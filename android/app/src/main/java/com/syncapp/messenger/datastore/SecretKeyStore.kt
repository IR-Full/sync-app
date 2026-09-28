package com.syncapp.messenger.datastore

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import com.syncapp.messenger.crypto.B64
import com.syncapp.messenger.crypto.Crypto
import com.syncapp.messenger.crypto.KeyPair
import com.syncapp.messenger.crypto.PinnedIdentity
import com.syncapp.messenger.crypto.SerializedSession
import com.syncapp.messenger.crypto.SigningKeyPair
import com.syncapp.messenger.crypto.TrustStore
import com.syncapp.messenger.data.secret.SecretMessageRow
import com.syncapp.messenger.di.SecretPreferences
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.first
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * Long-term secret-chat material for this device.
 *
 * **Everything written here is encrypted first** with the non-extractable
 * AndroidKeyStore key behind [TokenCipher]. A DataStore file is app-private,
 * which is enough against another app on a healthy device and nothing at all
 * against a rooted phone or an ADB backup — and unlike a session token, a
 * ratchet identity cannot be re-minted by logging in again. Whoever reads it can
 * decrypt every secret conversation this device has had and impersonate it to
 * every peer.
 *
 * The web client cannot do this: the browser has no key store it can use, so
 * `client/src/entities/secret-chat` keeps its keys readable by any script in the
 * origin, and says so. On Android the platform offers a better answer, so the
 * better answer is what is used.
 *
 * Three things live here, in one file because they share a lifetime — they all
 * belong to one account on one device, and all three go together on logout:
 *
 *  - the identity (X25519 + Ed25519 long-term keys, signed prekey, one-time prekeys)
 *  - one ratchet session per peer device
 *  - the TOFU pins for peer devices
 *  - the transcripts themselves, which exist nowhere else: the server relays
 *    ciphertext and stores nothing, so plaintext at rest here would be the only
 *    unprotected copy of a conversation deliberately kept from the server
 */
private const val ONE_TIME_PREKEY_COUNT = 100

/** Top back up once the pool falls below this. */
private const val ONE_TIME_PREKEY_LOW_WATER = ONE_TIME_PREKEY_COUNT / 4

/**
 * How long a signed prekey is used before a fresh one replaces it.
 *
 * The signed prekey is what an initiator does DH against when starting a
 * session, so a device that never rotates it has one key standing behind every
 * session it will ever receive, forever. Seven days is the interval Signal
 * documents.
 */
private const val SIGNED_PREKEY_MAX_AGE_MS = 7L * 24 * 60 * 60 * 1000

/**
 * Ceiling on prekeys kept LOCALLY, mirroring the directory's own cap.
 *
 * The pool has to be able to grow past [ONE_TIME_PREKEY_COUNT]: when the directory
 * reports it is running low, the keys it is missing are ones it has already served, so
 * topping it up means minting NEW pairs while the old private halves are still needed
 * for first messages in flight. Unbounded, that grows by a batch every time the device
 * gets popular. Over the cap the OLDEST go, which is what the directory does with them
 * too, so the key dropped here is the one it dropped there.
 */
private const val MAX_LOCAL_PREKEYS = 256

/**
 * What the directory reports back on KEY_PUBLISH.
 *
 * The local pool cannot stand in for it. One-time prekeys are consumed by PEERS fetching
 * bundles, and the pool here shrinks only when a message actually decrypts with a key -
 * which misses every fetch that never became a message: a peer that gave up, or a
 * multi-device fan-out another device answered. Left to the local count the directory
 * empties while this device believes it is full, and every session started afterwards
 * silently uses three Diffie-Hellmans instead of four.
 */
data class DirectoryKeyState(
    /** prekeys the directory holds AFTER the publish that reported this */
    val oneTimePreKeysLeft: Int,
    /** age of the signed prekey the DIRECTORY holds; 0 when it has just been set */
    val signedPreKeyAgeMs: Long,
    /** how many prekeys from the reporting frame the directory kept */
    val accepted: Int,
)

@Serializable
private data class StoredPair(val privateKey: String, val publicKey: String)

@Serializable
private data class StoredIdentity(
    val identity: StoredPair,
    val signing: StoredPair,
    val signedPreKey: StoredPair,
    val signedPreKeyCreatedAt: Long,
    /**
     * The signed prekey used BEFORE the last rotation.
     *
     * Kept for one generation because rotation is not atomic across the network:
     * a peer may have fetched the old bundle seconds before the rotation and be
     * about to send its first message against it. The relay is fire-and-forget,
     * so without a grace copy that message is simply lost and the sender is
     * never told.
     */
    val previousSignedPreKey: StoredPair? = null,
    val oneTimePreKeys: List<StoredPair> = emptyList(),
    /**
     * Public halves (base64) the directory has CONFIRMED it stored.
     *
     * Publishing APPENDS on the server rather than replacing, so resending the whole
     * pool on every connect - which is what this did - filed each public key again, and
     * a one-time prekey stored twice can be handed to two peers, which is the one thing
     * it exists not to be. Absent on an identity written before this was tracked, which
     * reads as "offer them again": one last duplicated batch on that device, against a
     * directory that caps and trims, in exchange for exact accounting from then on.
     */
    val publishedPreKeys: List<String> = emptyList(),
)

/** This device's long-term secret-chat identity, decoded. */
data class SecretIdentity(
    val identity: KeyPair,
    val signing: SigningKeyPair,
    val signedPreKey: KeyPair,
    val signedPreKeyCreatedAt: Long,
    val previousSignedPreKey: KeyPair?,
    val oneTimePreKeys: List<KeyPair>,
    /** base64 public halves the directory has confirmed; see [StoredIdentity]. */
    val publishedPreKeys: Set<String> = emptySet(),
) {
    /** Prekeys the directory has not acknowledged - what a publish should carry. */
    val unpublishedPreKeys: List<KeyPair>
        get() = oneTimePreKeys.filterNot { B64.encode(it.publicKey) in publishedPreKeys }
}

@Singleton
class SecretKeyStore @Inject constructor(
    @param:SecretPreferences private val store: DataStore<Preferences>,
    private val cipher: TokenCipher,
) {
    private val json = Json { ignoreUnknownKeys = true }

    private val identityKey = stringPreferencesKey("identity")
    private val sessionsKey = stringPreferencesKey("sessions")
    private val pinsKey = stringPreferencesKey("pins")
    private val transcriptsKey = stringPreferencesKey("transcripts")

    /**
     * Loads this device's identity, minting one on first use.
     *
     * Generating 100 one-time prekeys is ~100 X25519 keygens, which is a few
     * milliseconds — worth doing once rather than running out after eight
     * incoming conversations and silently completing X3DH without a one-time
     * prekey, which is measurably weaker and reports nothing.
     */
    suspend fun identity(): SecretIdentity {
        readIdentity()?.let { return it }
        val fresh = SecretIdentity(
            identity = Crypto.generateKeyPair(),
            signing = Crypto.generateSigningKeyPair(),
            signedPreKey = Crypto.generateKeyPair(),
            signedPreKeyCreatedAt = System.currentTimeMillis(),
            previousSignedPreKey = null,
            oneTimePreKeys = List(ONE_TIME_PREKEY_COUNT) { Crypto.generateKeyPair() },
            publishedPreKeys = emptySet(),
        )
        writeIdentity(fresh)
        return fresh
    }

    /**
     * Rotates the signed prekey if it has aged out and refills the one-time pool
     * if it has run low. Returns true when anything changed, so the caller knows
     * to republish.
     */
    suspend fun maintain(): Boolean {
        val current = identity()
        var next = current
        var changed = false

        if (System.currentTimeMillis() - current.signedPreKeyCreatedAt >= SIGNED_PREKEY_MAX_AGE_MS) {
            next = next.copy(
                // The outgoing key becomes the grace copy; the one it replaces is
                // dropped, which is the point — a key kept forever never rotated.
                previousSignedPreKey = next.signedPreKey,
                signedPreKey = Crypto.generateKeyPair(),
                signedPreKeyCreatedAt = System.currentTimeMillis(),
            )
            changed = true
        }

        if (next.oneTimePreKeys.size <= ONE_TIME_PREKEY_LOW_WATER) {
            val missing = ONE_TIME_PREKEY_COUNT - next.oneTimePreKeys.size
            next = next.copy(
                oneTimePreKeys = next.oneTimePreKeys + List(missing) { Crypto.generateKeyPair() },
            )
            changed = true
        }

        if (changed) writeIdentity(next)
        return changed
    }

    /**
     * Folds the directory's own report of this device's bundle back into stored state.
     *
     * [offered] is the prekey list of the publish being answered, in the order it went
     * out, so the keys the directory kept can be marked as offered. Returns true when the
     * bundle has to go out again - because the directory is running low and fresh keys
     * were minted for it, or because what it holds is older than the rotation window and
     * this device's last publish evidently never landed.
     */
    suspend fun applyDirectoryState(state: DirectoryKeyState, offered: List<String>): Boolean {
        val current = readIdentity() ?: return false

        // The survivors of a truncated frame are its LAST `accepted` keys: the directory
        // appends and trims from the front, so the newest are the ones that stay. Marking
        // the first ones instead would retire exactly the keys it dropped and re-offer the
        // ones it kept - the duplicate this tracking prevents, in the one case where it is
        // certain to happen.
        val accepted = state.accepted.coerceIn(0, offered.size)
        var next = current.copy(
            publishedPreKeys = current.publishedPreKeys + offered.takeLast(accepted),
        )

        // Republishing is driven by what the DIRECTORY reports, never by what is still
        // unpublished here: an unpublished remainder means the per-publish cap truncated
        // the frame, and republishing on that would send the remainder, have it truncated
        // again, and loop forever against a directory that is already full.
        var republish = false

        if (state.oneTimePreKeysLeft <= ONE_TIME_PREKEY_LOW_WATER) {
            // Fresh pairs, not the ones already in the pool: the keys the directory is
            // missing are precisely the ones it has already handed out.
            val missing = ONE_TIME_PREKEY_COUNT - state.oneTimePreKeysLeft
            next = next.copy(
                oneTimePreKeys = next.oneTimePreKeys + List(missing) { Crypto.generateKeyPair() },
            )
            republish = true
        }

        if (next.oneTimePreKeys.size > MAX_LOCAL_PREKEYS) {
            val dropped = next.oneTimePreKeys.take(next.oneTimePreKeys.size - MAX_LOCAL_PREKEYS)
            val gone = dropped.map { B64.encode(it.publicKey) }.toSet()
            next = next.copy(
                oneTimePreKeys = next.oneTimePreKeys.takeLast(MAX_LOCAL_PREKEYS),
                // The bookkeeping shrinks with the pool, or it grows without bound on its
                // own: a base64 string for every key this device ever offered.
                publishedPreKeys = next.publishedPreKeys - gone,
            )
        }

        // The directory is serving a signed prekey older than the rotation window while
        // this device may well believe it rotated. Only a republish settles it: [maintain]
        // rotates a genuinely old local key on the next pass, and a fresh one simply goes
        // out again. Either way the directory's next report reads 0.
        if (state.signedPreKeyAgeMs >= SIGNED_PREKEY_MAX_AGE_MS) republish = true

        writeIdentity(next)
        return republish
    }

    /**
     * Forgets a one-time prekey once it has been used.
     *
     * The protocol never tells the publisher which prekey a fetcher consumed, so
     * "used" is only discovered when a message actually decrypts with it.
     * Dropping it then keeps the candidate list short and stops it being reused,
     * which would defeat the forward secrecy it exists to provide.
     */
    suspend fun dropOneTimePreKey(publicKey: ByteArray) {
        val current = readIdentity() ?: return
        val remaining = current.oneTimePreKeys.filterNot { it.publicKey.contentEquals(publicKey) }
        if (remaining.size == current.oneTimePreKeys.size) return
        // The mark goes with the key. Left behind, the set grows by every prekey this
        // device ever offered and nothing would ever remove an entry whose key is gone.
        writeIdentity(
            current.copy(
                oneTimePreKeys = remaining,
                publishedPreKeys = current.publishedPreKeys - B64.encode(publicKey),
            ),
        )
    }

    // --- ratchet sessions, keyed "userId:deviceId" ---

    suspend fun sessions(): Map<String, SerializedSession> =
        decodeOrEmpty(sessionsKey) { json.decodeFromString(it) }

    suspend fun saveSession(key: String, session: SerializedSession) {
        val next = sessions() + (key to session)
        writeEncrypted(sessionsKey, json.encodeToString(next))
    }

    // --- TOFU pins ---

    suspend fun trustStore(): TrustStore =
        TrustStore(decodeOrEmpty(pinsKey) { json.decodeFromString<Map<String, PinnedIdentity>>(it) })

    suspend fun saveTrustStore(trust: TrustStore) {
        writeEncrypted(pinsKey, json.encodeToString(trust.snapshot()))
    }

    // --- transcripts, keyed by peer user id ---

    suspend fun transcripts(): Map<String, List<SecretMessageRow>> {
        val raw = readDecrypted(transcriptsKey) ?: return emptyMap()
        return runCatching {
            json.decodeFromString<Map<String, List<SecretMessageRow>>>(raw)
        }.getOrDefault(emptyMap())
    }

    suspend fun saveTranscripts(value: Map<String, List<SecretMessageRow>>) {
        writeEncrypted(transcriptsKey, json.encodeToString(value))
    }

    /** Everything here belongs to the account that just left. */
    suspend fun clear() {
        store.edit { it.clear() }
    }

    // --- storage plumbing ---

    private suspend fun readIdentity(): SecretIdentity? {
        val raw = readDecrypted(identityKey) ?: return null
        val stored = runCatching { json.decodeFromString<StoredIdentity>(raw) }.getOrNull()
            ?: return null
        return runCatching {
            SecretIdentity(
                identity = stored.identity.toKeyPair(),
                signing = SigningKeyPair(
                    B64.decodeOrNull(stored.signing.privateKey)!!,
                    B64.decodeOrNull(stored.signing.publicKey)!!,
                ),
                signedPreKey = stored.signedPreKey.toKeyPair(),
                signedPreKeyCreatedAt = stored.signedPreKeyCreatedAt,
                previousSignedPreKey = stored.previousSignedPreKey?.toKeyPair(),
                oneTimePreKeys = stored.oneTimePreKeys.map { it.toKeyPair() },
                publishedPreKeys = stored.publishedPreKeys.toSet(),
            )
        }.getOrNull()
    }

    private suspend fun writeIdentity(identity: SecretIdentity) {
        val stored = StoredIdentity(
            identity = identity.identity.toStored(),
            signing = StoredPair(
                B64.encode(identity.signing.privateKey),
                B64.encode(identity.signing.publicKey),
            ),
            signedPreKey = identity.signedPreKey.toStored(),
            signedPreKeyCreatedAt = identity.signedPreKeyCreatedAt,
            previousSignedPreKey = identity.previousSignedPreKey?.toStored(),
            oneTimePreKeys = identity.oneTimePreKeys.map { it.toStored() },
            publishedPreKeys = identity.publishedPreKeys.sorted(),
        )
        writeEncrypted(identityKey, json.encodeToString(stored))
    }

    private suspend fun <T> decodeOrEmpty(
        key: Preferences.Key<String>,
        decode: (String) -> Map<String, T>,
    ): Map<String, T> {
        val raw = readDecrypted(key) ?: return emptyMap()
        // A value that will not parse is treated as absent rather than fatal: the
        // worst case is a re-handshake, and crashing on launch over a corrupt
        // cache would be a far worse outcome than losing it.
        return runCatching { decode(raw) }.getOrDefault(emptyMap())
    }

    private suspend fun readDecrypted(key: Preferences.Key<String>): String? {
        val stored = store.data.first()[key] ?: return null
        return cipher.decrypt(stored).ifEmpty { null }
    }

    private suspend fun writeEncrypted(key: Preferences.Key<String>, value: String) {
        val sealed = cipher.encrypt(value)
        store.edit { it[key] = sealed }
    }

    private fun StoredPair.toKeyPair() =
        KeyPair(B64.decodeOrNull(privateKey)!!, B64.decodeOrNull(publicKey)!!)

    private fun KeyPair.toStored() = StoredPair(B64.encode(privateKey), B64.encode(publicKey))
}
