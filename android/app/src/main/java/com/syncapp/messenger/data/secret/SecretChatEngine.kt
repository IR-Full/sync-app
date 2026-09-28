package com.syncapp.messenger.data.secret

import com.syncapp.messenger.crypto.B64
import com.syncapp.messenger.crypto.BadPreKeySignatureException
import com.syncapp.messenger.crypto.InitiatorKeys
import com.syncapp.messenger.crypto.PreKeyBundle
import com.syncapp.messenger.crypto.RatchetHeaderCodec
import com.syncapp.messenger.crypto.RatchetSession
import com.syncapp.messenger.crypto.ResponderKeys
import com.syncapp.messenger.crypto.Safety
import com.syncapp.messenger.crypto.TrustVerdict
import com.syncapp.messenger.crypto.X3dh
import com.syncapp.messenger.datastore.DirectoryKeyState
import com.syncapp.messenger.datastore.SecretIdentity
import com.syncapp.messenger.datastore.SecretKeyStore
import com.syncapp.messenger.network.GatewayRequests
import com.syncapp.messenger.network.protocol.KeyBundle
import com.syncapp.messenger.network.protocol.KeyBundles
import com.syncapp.messenger.network.protocol.KeyFetch
import com.syncapp.messenger.network.protocol.KeyPublish
import com.syncapp.messenger.network.protocol.KeyState
import com.syncapp.messenger.network.protocol.MsgType
import com.syncapp.messenger.network.protocol.SecretMsg
import com.syncapp.messenger.network.request
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * Secret chats: the end-to-end encrypted side channel.
 *
 * Deliberately separate from the ordinary message path, because it is a
 * different thing: these messages never reach the server in readable form, are
 * not stored anywhere, and exist only on the devices that received them. The
 * gateway relays opaque bytes and holds only public prekeys.
 *
 * Multi-device is this layer's job. The relay addresses ONE device, so a message
 * to a person is really N ciphertexts, each under its own ratchet — a ciphertext
 * sealed for one device cannot be opened by another.
 */

/** The X3DH bootstrap carried inside `SecretMsg.ratchetHeader`. */
@Serializable
private data class InitEnvelope(
    /** base64 X25519 identity key; present only on a session's first message */
    val ik: String? = null,
    /** base64 X25519 ephemeral key; likewise */
    val ek: String? = null,
    /** base64 ratchet header — always present */
    val rh: String,
)

/** One decrypted inbound secret message. */
data class OpenedSecret(
    val fromUserId: String,
    val fromDeviceId: String,
    val plaintext: String,
)

/**
 * A peer device's long-term identity no longer matches what was pinned.
 *
 * Thrown rather than logged because the caller has to stop: continuing would
 * encrypt to whatever key the directory just supplied, which is the attack the
 * pin exists to catch. A reinstall and an attack look identical from here, so
 * the human decides — through the panel that shows the safety number.
 */
class IdentityChangedException(val userId: String, val deviceId: String) :
    Exception("e2e: the peer device identity changed")

/** The peer has published no prekeys, so there is nothing to encrypt to. */
class NoSecretDevicesException : Exception("e2e: the peer has no devices with published keys")

@Singleton
class SecretChatEngine @Inject constructor(
    private val gateway: GatewayRequests,
    private val keys: SecretKeyStore,
) {
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = false }

    private fun sessionKey(userId: String, deviceId: String) = "$userId:$deviceId"

    /**
     * Publishes this device's prekey bundle, rotating first if anything has aged
     * out, and folds the directory's answer back in.
     *
     * Maintenance runs before the publish rather than on its own schedule: both
     * change what the directory should be serving, and doing them apart leaves a
     * window where this device's stored keys and the directory's copy disagree.
     * The disagreement is silent — the relay is fire-and-forget, so a peer that
     * fetched a stale bundle just sends something nobody can open.
     *
     * KEY_PUBLISH is a REQUEST now, not fire-and-forget. The KEY_STATE reply is the only
     * place this device can learn its own one-time prekey balance: those keys are
     * consumed by PEERS fetching bundles, so nothing observable here says the batch is
     * running out, and an empty batch silently costs every new session the stronger
     * four-DH handshake. A second pass runs when the answer asks for more — bounded,
     * because the keys that pass would send were already minted by the first.
     */
    suspend fun publishKeys() {
        keys.maintain()
        if (publishOnce()) publishOnce()
    }

    /** One publish. Returns true when the directory's answer asks for another. */
    private suspend fun publishOnce(): Boolean {
        val identity = keys.identity()
        // Only what the directory has not acknowledged, in a stable order so the reply's
        // `accepted` count can be matched back to the keys it refers to. Publishing
        // APPENDS on the server, so re-offering a stored key files it twice - and a
        // one-time prekey stored twice can be handed to two peers.
        val offered = identity.unpublishedPreKeys.map { B64.encode(it.publicKey) }
        val state = gateway.request<KeyState>(
            MsgType.KEY_PUBLISH,
            KeyPublish(
                identityKey = B64.encode(identity.identity.publicKey),
                signingKey = B64.encode(identity.signing.publicKey),
                signedPrekey = B64.encode(identity.signedPreKey.publicKey),
                // Signed with the Ed25519 identity key so peers can prove the
                // prekey really came from us — the MITM defence against a
                // hostile directory.
                signedPrekeySig = B64.encode(
                    com.syncapp.messenger.crypto.Crypto.signPreKey(
                        identity.signing.privateKey,
                        identity.signedPreKey.publicKey,
                    ),
                ),
                prekeys = offered,
            ),
        )
        return keys.applyDirectoryState(
            DirectoryKeyState(
                oneTimePreKeysLeft = state.oneTimePrekeysLeft,
                signedPreKeyAgeMs = state.signedPrekeyAgeMs,
                accepted = state.accepted,
            ),
            offered,
        )
    }

    /**
     * Sends one message to every device the peer has published keys for.
     *
     * The pin is checked BEFORE any key material from a bundle is used: once the
     * shared secret is derived the ciphertext is already readable by whoever
     * supplied the key, so catching a substitution afterwards catches nothing.
     */
    suspend fun send(peerUserId: String, text: String) {
        val identity = keys.identity()
        val bundles = gateway.request<KeyBundles>(
            MsgType.KEY_FETCH_ALL,
            KeyFetch(userId = peerUserId, deviceId = ""),
        )
        if (bundles.bundles.isEmpty()) throw NoSecretDevicesException()

        val trust = keys.trustStore()
        var trustChanged = false

        for (bundle in bundles.bundles) {
            val deviceUserId = bundle.userId.ifEmpty { peerUserId }
            val verdict = trust.verify(
                deviceUserId,
                bundle.deviceId,
                bundle.identityKey,
                bundle.signingKey,
            )
            if (verdict is TrustVerdict.Changed) {
                throw IdentityChangedException(deviceUserId, bundle.deviceId)
            }

            val key = sessionKey(deviceUserId, bundle.deviceId)
            val stored = keys.sessions()[key]
            val session: RatchetSession
            val envelope: InitEnvelope

            if (stored != null) {
                session = RatchetSession.deserialize(stored) ?: continue
                val (header, ciphertext) = session.encrypt(text.toByteArray(Charsets.UTF_8))
                envelope = InitEnvelope(rh = B64.encode(RatchetHeaderCodec.marshal(header)))
                relay(deviceUserId, bundle.deviceId, envelope, ciphertext)
            } else {
                val ephemeral = com.syncapp.messenger.crypto.Crypto.generateKeyPair()
                val (sharedSecret, ephemeralPublic) = X3dh.initiator(
                    InitiatorKeys(identity.identity, ephemeral),
                    bundle.toPreKeyBundle() ?: continue,
                )
                session = RatchetSession.initiator(
                    sharedSecret,
                    B64.decodeOrNull(bundle.signedPrekey) ?: continue,
                )
                val (header, ciphertext) = session.encrypt(text.toByteArray(Charsets.UTF_8))
                envelope = InitEnvelope(
                    ik = B64.encode(identity.identity.publicKey),
                    ek = B64.encode(ephemeralPublic),
                    rh = B64.encode(RatchetHeaderCodec.marshal(header)),
                )
                relay(deviceUserId, bundle.deviceId, envelope, ciphertext)
            }

            keys.saveSession(key, session.serialize())
            if (verdict is TrustVerdict.FirstUse) {
                // Pin only after the session is established: pinning a key we
                // then failed to use would record an identity this device never
                // actually talked to.
                trust.accept(
                    deviceUserId,
                    bundle.deviceId,
                    bundle.identityKey,
                    bundle.signingKey,
                )
                trustChanged = true
            }
        }
        if (trustChanged) keys.saveTrustStore(trust)
    }

    /**
     * Opens one inbound secret message.
     *
     * Returns null rather than throwing for anything malformed. The header and
     * ciphertext are relay-supplied strings, so neither is guaranteed to be
     * base64 or a well-formed header; a throw here would escape into the frame
     * handler and take down the connection over one bad message.
     */
    suspend fun receive(message: SecretMsg): OpenedSecret? {
        val init = runCatching { json.decodeFromString<InitEnvelope>(message.ratchetHeader) }
            .getOrNull() ?: return null
        val headerBytes = B64.decodeOrNull(init.rh) ?: return null
        val header = RatchetHeaderCodec.unmarshal(headerBytes) ?: return null
        val ciphertext = B64.decodeOrNull(message.ciphertext) ?: return null

        val identity = keys.identity()
        val key = sessionKey(message.fromUserId, message.fromDeviceId)

        // An established session simply advances.
        keys.sessions()[key]?.let { stored ->
            RatchetSession.deserialize(stored)?.let { session ->
                runCatching { session.decrypt(header, ciphertext) }.getOrNull()?.let { plaintext ->
                    keys.saveSession(key, session.serialize())
                    return OpenedSecret(
                        message.fromUserId,
                        message.fromDeviceId,
                        String(plaintext, Charsets.UTF_8),
                    )
                }
            }
            // Fall through: the peer may have started a fresh session after a
            // reinstall, and failing outright would strand the conversation.
        }

        // Otherwise this must be a first message, carrying the X3DH bootstrap.
        val ik = init.ik?.let { B64.decodeOrNull(it) } ?: return null
        val ek = init.ek?.let { B64.decodeOrNull(it) } ?: return null

        return openAsResponder(identity, key, ik, ek, header, ciphertext, message)
    }

    /**
     * Tries every unconsumed one-time prekey, then the no-OPK case.
     *
     * The directory hands a fetcher one of our prekeys and never tells us which,
     * so the AEAD is the oracle: only the right key authenticates. With a
     * hundred prekeys this is a hundred X25519 operations on the FIRST message
     * of a session and nothing afterwards — and stopping at the first candidate,
     * which reads like an optimisation, would silently fail for every sender
     * that did not happen to take prekey zero.
     *
     * Both signed prekeys are tried too, current first. Rotation is not atomic
     * across the network: a peer may have fetched the previous bundle seconds
     * before this device rotated.
     */
    private suspend fun openAsResponder(
        identity: SecretIdentity,
        key: String,
        initiatorIdentity: ByteArray,
        initiatorEphemeral: ByteArray,
        header: com.syncapp.messenger.crypto.RatchetHeader,
        ciphertext: ByteArray,
        message: SecretMsg,
    ): OpenedSecret? {
        val signedPreKeys = listOfNotNull(identity.signedPreKey, identity.previousSignedPreKey)

        for (signedPreKey in signedPreKeys) {
            val candidates = identity.oneTimePreKeys.map { it as com.syncapp.messenger.crypto.KeyPair? } + listOf(null)
            for (oneTime in candidates) {
                val secret = runCatching {
                    X3dh.responder(
                        ResponderKeys(identity.identity, signedPreKey, oneTime),
                        initiatorIdentity,
                        initiatorEphemeral,
                        usedOneTime = oneTime != null,
                    )
                }.getOrNull() ?: continue

                val session = RatchetSession.responder(secret, signedPreKey)
                val plaintext = runCatching { session.decrypt(header, ciphertext) }.getOrNull()
                    ?: continue

                keys.saveSession(key, session.serialize())
                // Reusing a one-time prekey defeats exactly the forward secrecy
                // it exists to provide, and only the AEAD knew which one matched.
                oneTime?.let { keys.dropOneTimePreKey(it.publicKey) }
                return OpenedSecret(
                    message.fromUserId,
                    message.fromDeviceId,
                    String(plaintext, Charsets.UTF_8),
                )
            }
        }
        return null
    }

    /**
     * The safety numbers of a peer's devices.
     *
     * One number per DEVICE, not per person: the identity being verified belongs
     * to a device, and collapsing several into one figure would mean a reinstall
     * on one phone invalidates the verification of another.
     *
     * Fetched on demand rather than cached — the number is only meaningful at
     * the moment someone is comparing it, and a stale one would show a match
     * against keys no longer in use.
     */
    suspend fun safetyNumbers(selfUserId: String, peerUserId: String): List<DeviceSafety> {
        val identity = keys.identity()
        val local = Safety.Identity(
            stableId = selfUserId,
            identityKey = identity.identity.publicKey,
            signingKey = identity.signing.publicKey,
        )
        val bundles = gateway.request<KeyBundles>(
            MsgType.KEY_FETCH_ALL,
            KeyFetch(userId = peerUserId, deviceId = ""),
        )
        val trust = keys.trustStore()

        return bundles.bundles.mapNotNull { bundle ->
            val deviceUserId = bundle.userId.ifEmpty { peerUserId }
            val ik = B64.decodeOrNull(bundle.identityKey) ?: return@mapNotNull null
            val sk = B64.decodeOrNull(bundle.signingKey) ?: return@mapNotNull null
            DeviceSafety(
                userId = deviceUserId,
                deviceId = bundle.deviceId,
                number = Safety.number(local, Safety.Identity(deviceUserId, ik, sk)),
                verdict = trust.verify(
                    deviceUserId,
                    bundle.deviceId,
                    bundle.identityKey,
                    bundle.signingKey,
                ),
                identityKey = bundle.identityKey,
                signingKey = bundle.signingKey,
            )
        }
    }

    /**
     * Records the keys the human just looked at.
     *
     * Takes the material rather than re-deriving it, because what gets pinned
     * must be what was DISPLAYED: fetching again could pin a different key than
     * the one whose safety number was compared, which would defeat the
     * comparison.
     */
    suspend fun acceptIdentity(device: DeviceSafety) {
        val trust = keys.trustStore()
        trust.accept(device.userId, device.deviceId, device.identityKey, device.signingKey)
        keys.saveTrustStore(trust)
    }

    private fun relay(
        toUserId: String,
        toDeviceId: String,
        envelope: InitEnvelope,
        ciphertext: ByteArray,
    ) {
        gateway.send(
            MsgType.SECRET_SEND,
            SecretMsg(
                toUserId = toUserId,
                toDeviceId = toDeviceId,
                ratchetHeader = json.encodeToString(envelope),
                ciphertext = B64.encode(ciphertext),
            ),
        )
    }

    private fun KeyBundle.toPreKeyBundle(): PreKeyBundle? {
        val ik = B64.decodeOrNull(identityKey) ?: return null
        val sk = B64.decodeOrNull(signingKey) ?: return null
        val spk = B64.decodeOrNull(signedPrekey) ?: return null
        val sig = B64.decodeOrNull(signedPrekeySig) ?: return null
        val otp = if (oneTimePrekey.isEmpty()) ByteArray(0) else B64.decodeOrNull(oneTimePrekey)
            ?: ByteArray(0)
        return PreKeyBundle(ik, sk, spk, sig, otp)
    }
}

/** One peer device's safety number and what pinning thinks of its current keys. */
data class DeviceSafety(
    val userId: String,
    val deviceId: String,
    /** the 60-digit number both sides should see identically */
    val number: String,
    val verdict: TrustVerdict,
    /** the exact material the number was computed from, so accepting pins it */
    val identityKey: String,
    val signingKey: String,
)
