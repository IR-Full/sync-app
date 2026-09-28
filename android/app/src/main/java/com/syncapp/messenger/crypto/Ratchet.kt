package com.syncapp.messenger.crypto

import kotlinx.serialization.Serializable
import org.bouncycastle.crypto.modes.ChaCha20Poly1305
import org.bouncycastle.crypto.params.AEADParameters
import org.bouncycastle.crypto.params.KeyParameter

/**
 * The Double Ratchet, ported from `server/pkg/e2e/ratchet.go`.
 *
 * Forward secrecy (a leaked key does not open past messages) plus
 * post-compromise security (a DH ratchet step heals the session). The server is
 * a blind relay throughout — it moves ciphertext and a header and can decrypt
 * neither.
 *
 * The header is authenticated, not encrypted: it travels as additional data for
 * the AEAD, so tampering with the ratchet key or counters fails decryption.
 */

/** Bounds the work a hostile header can force by claiming a huge gap, per call. */
private const val MAX_SKIP = 1000

/**
 * Bounds how many skipped message keys a session RETAINS.
 *
 * `MAX_SKIP` only ever bounded a single call: every DH ratchet step restarts the
 * count, so the map would grow without limit — and this state is persisted, so
 * the growth would outlive the process.
 */
private const val MAX_SKIPPED_KEYS = 2 * MAX_SKIP

private val RK_INFO = "SyncApp-Ratchet-RK".toByteArray(Charsets.UTF_8)
private val MSG_INFO = "SyncApp-Ratchet-Msg".toByteArray(Charsets.UTF_8)

/** Travels (authenticated but not encrypted) with each ciphertext. */
data class RatchetHeader(
    /** sender's current ratchet public key */
    val dh: ByteArray,
    /** number of messages in the previous sending chain */
    val pn: Int,
    /** message number in the current sending chain */
    val n: Int,
) {
    /**
     * The exact serialisation this header travels as — the bytes [unmarshal]
     * parsed it from, or the bytes [Session.encrypt] authenticated. Null for a
     * header built from values alone, which [RatchetHeaderCodec.marshal] then
     * encodes canonically.
     *
     * This makes the AEAD's additional data literally the bytes on the wire, in
     * both directions, rather than a re-encoding that merely ought to match
     * them. Four implementations produce this header — Go, TypeScript, Kotlin,
     * Swift — and each receiver used to parse the header and then re-serialise
     * it to rebuild the AD. That works only while all four emit byte-identical
     * canonical JSON: same field order, no whitespace, the same base64 alphabet
     * and padding. Nothing enforces it, and the day one diverges every message
     * between the two versions fails with [DecryptException] —
     * indistinguishable from a forgery, and pointing at nothing. Carrying the
     * bytes removes the requirement instead of documenting it.
     *
     * Declared in the class BODY rather than the primary constructor, and that
     * is the safety property, not a style choice. The bytes and the parsed
     * counters must never disagree: the ratchet drives its state from
     * `dh`/`pn`/`n` while the AEAD verifies the bytes, so a header whose fields
     * say one thing and whose bytes say another would advance the session on
     * values nobody authenticated. `copy()` calls the primary constructor, so a
     * header derived with `copy(pn = 99)` starts with no bytes of its own and is
     * encoded from the counters it actually carries — and the AEAD rejects it.
     * `Header.raw` in `server/pkg/e2e/ratchet.types.go` gets the same guarantee
     * from being unexported.
     */
    internal var wireBytes: ByteArray? = null
        private set

    /** Records the bytes this header is authenticated as. Returns itself. */
    internal fun withWireBytes(bytes: ByteArray): RatchetHeader = apply {
        // Copied: the caller owns the array and this reference outlives the call.
        wireBytes = bytes.copyOf()
    }

    // equals/hashCode cover the three wire fields only. wireBytes is derived
    // from them for any header that has it, so including it would either be
    // redundant or would make a parsed header unequal to the same header built
    // from its values.
    override fun equals(other: Any?): Boolean =
        this === other ||
            (other is RatchetHeader && dh.contentEquals(other.dh) && pn == other.pn && n == other.n)

    override fun hashCode(): Int = 31 * (31 * dh.contentHashCode() + pn) + n
}

class DecryptException : Exception("e2e: decryption failed")

/** A session serialised for persistence across restarts. */
@Serializable
data class SerializedSession(
    val dhsPrivate: String,
    val dhsPublic: String,
    val dhr: String? = null,
    val rootKey: String,
    val sendingChainKey: String? = null,
    val receivingChainKey: String? = null,
    val sent: Int = 0,
    val received: Int = 0,
    val previousSent: Int = 0,
    val skipped: Map<String, String> = emptyMap(),
    /** insertion order of [skipped], so the oldest key is the one evicted */
    val skippedOrder: List<String> = emptyList(),
)

object RatchetHeaderCodec {
    /**
     * The header's wire form, and therefore the AEAD's additional data.
     *
     * A header that carries its own bytes ([RatchetHeader.wireBytes]) is
     * returned as it stands: one that arrived keeps what the peer signed, one
     * this process built keeps what it authenticated. Only a header with none is
     * encoded here, and that encoding matches Go's `json.Marshal` — standard
     * base64 for the key, fields in declaration order (dh, pn, n), no
     * whitespace. Matching still matters for a header this side originates, but
     * it is no longer the only thing holding interop together.
     *
     * Hand-built rather than handed to a JSON library on purpose — a library is
     * free to reorder keys, add spacing or escape differently, and any of those
     * changes the authenticated bytes without changing the meaning.
     */
    fun marshal(header: RatchetHeader): ByteArray =
        header.wireBytes
            ?: """{"dh":"${B64.encode(header.dh)}","pn":${header.pn},"n":${header.n}}"""
                .toByteArray(Charsets.UTF_8)

    /**
     * Parses a header from the wire, remembering the bytes it came from so they
     * — and not a re-encoding of them — are what gets authenticated. Returns
     * null for anything malformed.
     */
    fun unmarshal(bytes: ByteArray): RatchetHeader? {
        val text = runCatching { String(bytes, Charsets.UTF_8) }.getOrNull() ?: return null
        val dh = Regex("\"dh\"\\s*:\\s*\"([^\"]*)\"").find(text)?.groupValues?.get(1)
        val pn = Regex("\"pn\"\\s*:\\s*(-?\\d+)").find(text)?.groupValues?.get(1)
        val n = Regex("\"n\"\\s*:\\s*(-?\\d+)").find(text)?.groupValues?.get(1)
        val dhBytes = dh?.let { B64.decodeOrNull(it) } ?: return null
        // Absent counters read as 0, matching Go's zero value for an omitted
        // field; a NEGATIVE one is malformed and must not become a huge gap
        // after the unsigned conversion the skip loop does.
        val pnValue = pn?.toIntOrNull() ?: 0
        val nValue = n?.toIntOrNull() ?: 0
        if (pnValue < 0 || nValue < 0) return null
        return RatchetHeader(dhBytes, pnValue, nValue).withWireBytes(bytes)
    }
}

/**
 * One Double Ratchet session with one peer device.
 *
 * Not safe for concurrent use — callers serialise per session, exactly as the
 * Go implementation requires.
 */
class RatchetSession private constructor(
    private var dhs: KeyPair,
    private var dhr: ByteArray?,
    private var rootKey: ByteArray,
) {
    private var sendingChainKey: ByteArray? = null
    private var receivingChainKey: ByteArray? = null
    private var sent = 0
    private var received = 0
    private var previousSent = 0
    private val skipped = LinkedHashMap<String, ByteArray>()
    private val skippedOrder = ArrayDeque<String>()

    companion object {
        /**
         * Initiator's session. The peer's signed prekey is the first DHr, and one
         * DH ratchet runs immediately so the initiator can send straight away.
         */
        fun initiator(sharedSecret: ByteArray, theirSignedPreKey: ByteArray): RatchetSession {
            val session = RatchetSession(Crypto.generateKeyPair(), theirSignedPreKey, sharedSecret)
            val dhOut = Crypto.diffieHellman(session.dhs.privateKey, theirSignedPreKey)
            val (rk, ck) = kdfRootKey(session.rootKey, dhOut)
            session.rootKey = rk
            session.sendingChainKey = ck
            return session
        }

        /**
         * Responder's session. It has no sending chain until the first message
         * arrives and triggers a ratchet step.
         */
        fun responder(sharedSecret: ByteArray, signedPreKey: KeyPair): RatchetSession =
            RatchetSession(signedPreKey, null, sharedSecret)

        fun deserialize(state: SerializedSession): RatchetSession? {
            val dhsPrivate = B64.decodeOrNull(state.dhsPrivate) ?: return null
            val dhsPublic = B64.decodeOrNull(state.dhsPublic) ?: return null
            val rootKey = B64.decodeOrNull(state.rootKey) ?: return null
            val session = RatchetSession(
                KeyPair(dhsPrivate, dhsPublic),
                state.dhr?.let { B64.decodeOrNull(it) },
                rootKey,
            )
            session.sendingChainKey = state.sendingChainKey?.let { B64.decodeOrNull(it) }
            session.receivingChainKey = state.receivingChainKey?.let { B64.decodeOrNull(it) }
            session.sent = state.sent
            session.received = state.received
            session.previousSent = state.previousSent
            for ((key, value) in state.skipped) {
                B64.decodeOrNull(value)?.let { session.skipped[key] = it }
            }
            // A session persisted before eviction existed has no recorded order.
            // The map's own insertion order is exactly right: it was filled in
            // ascending message number, which is the order to evict in.
            session.skippedOrder.addAll(
                state.skippedOrder.ifEmpty { session.skipped.keys.toList() },
            )
            return session
        }

        /** Derives (rootKey, chainKey) from the root key and a DH output. */
        private fun kdfRootKey(rootKey: ByteArray, dhOut: ByteArray): Pair<ByteArray, ByteArray> {
            val out = Crypto.hkdf(dhOut, rootKey, RK_INFO, 64)
            return out.copyOfRange(0, 32) to out.copyOfRange(32, 64)
        }

        /** Advances a chain key; the constants 0x02/0x01 are part of the contract. */
        private fun kdfChainKey(chainKey: ByteArray): Pair<ByteArray, ByteArray> {
            val next = Crypto.hmacSha256(chainKey, byteArrayOf(0x02))
            val messageKey = Crypto.hmacSha256(chainKey, byteArrayOf(0x01))
            return next to messageKey
        }

        /**
         * Derives the AEAD key and nonce from a message key.
         *
         * A fixed nonce is safe here precisely because the key is derived per
         * message and never reused — the message key itself is deliberately not
         * used as the cipher key.
         */
        private fun aeadFor(messageKey: ByteArray): Pair<ByteArray, ByteArray> {
            val buf = Crypto.hkdf(messageKey, null, MSG_INFO, 32 + 12)
            return buf.copyOfRange(0, 32) to buf.copyOfRange(32, 44)
        }

        private fun seal(messageKey: ByteArray, ad: ByteArray, plaintext: ByteArray): ByteArray {
            val (key, nonce) = aeadFor(messageKey)
            val aead = ChaCha20Poly1305()
            aead.init(true, AEADParameters(KeyParameter(key), 128, nonce, ad))
            val out = ByteArray(aead.getOutputSize(plaintext.size))
            var len = aead.processBytes(plaintext, 0, plaintext.size, out, 0)
            len += aead.doFinal(out, len)
            return out.copyOf(len)
        }

        private fun open(messageKey: ByteArray, ad: ByteArray, ciphertext: ByteArray): ByteArray? =
            runCatching {
                val (key, nonce) = aeadFor(messageKey)
                val aead = ChaCha20Poly1305()
                aead.init(false, AEADParameters(KeyParameter(key), 128, nonce, ad))
                val out = ByteArray(aead.getOutputSize(ciphertext.size))
                var len = aead.processBytes(ciphertext, 0, ciphertext.size, out, 0)
                len += aead.doFinal(out, len)
                out.copyOf(len)
            }.getOrNull()

        /** Storage key for a skipped message key. Internal only — never on the wire. */
        private fun skippedKey(dhPublic: ByteArray, n: Int): String = "${B64.encode(dhPublic)}|$n"
    }

    fun encrypt(plaintext: ByteArray): Pair<RatchetHeader, ByteArray> {
        val chain = sendingChainKey ?: error("e2e: no sending chain")
        val (nextChain, messageKey) = kdfChainKey(chain)
        sendingChainKey = nextChain

        val header = RatchetHeader(dh = dhs.publicKey, pn = previousSent, n = sent)
        sent++
        // Pin the serialisation used as additional data to this header, so whatever
        // the caller puts on the wire is byte-for-byte what this AEAD authenticated.
        val ad = RatchetHeaderCodec.marshal(header)
        header.withWireBytes(ad)
        return header to seal(messageKey, ad, plaintext)
    }

    /**
     * Opens one inbound message.
     *
     * **No state moves until the AEAD says the message is genuine.** That
     * ordering is the security property, not a tidiness preference: the header
     * is written by whoever sent the frame, and the relay lets any account
     * address any device. Ratcheting first and authenticating afterwards means a
     * single forged frame carrying a random ratchet key rewrites the session —
     * breaking the real conversation and costing up to `2 * MAX_SKIP` HMAC
     * derivations on the way.
     */
    fun decrypt(header: RatchetHeader, ciphertext: ByteArray): ByteArray {
        // 1. A key stored for a message that arrived out of order. Consuming one
        //    is already commit-on-success, so it needs no staging.
        trySkipped(header, ciphertext)?.let { return it }

        // 2. The ordinary case: the peer's current ratchet key, the next message
        //    in the chain. Only the receiving chain moves, so it is held in
        //    locals until the message authenticates — and this branch, which
        //    every in-order message takes, never pays to copy the skipped map.
        val chain = receivingChainKey
        if (chain != null && sameDhr(header.dh) && header.n == received) {
            val (nextChain, messageKey) = kdfChainKey(chain)
            val plaintext = open(messageKey, RatchetHeaderCodec.marshal(header), ciphertext)
                ?: throw DecryptException()
            receivingChainKey = nextChain
            received++
            return plaintext
        }

        // 3. Everything else — a DH ratchet step, a gap to skip, or both — runs
        //    on a COPY, adopted only if the frame authenticates.
        val trial = deserialize(serialize()) ?: throw DecryptException()
        val plaintext = trial.advance(header, ciphertext)
        adopt(trial)
        return plaintext
    }

    /**
     * The ratchet+skip path, run against a throwaway copy by [decrypt]. It may
     * mutate freely: nothing it touches is the caller's session until [adopt].
     */
    private fun advance(header: RatchetHeader, ciphertext: ByteArray): ByteArray {
        if (!sameDhr(header.dh)) {
            skipMessageKeys(header.pn)
            dhRatchet(header)
        }
        skipMessageKeys(header.n)

        val chain = receivingChainKey ?: throw DecryptException()
        val (nextChain, messageKey) = kdfChainKey(chain)
        receivingChainKey = nextChain
        received++
        return open(messageKey, RatchetHeaderCodec.marshal(header), ciphertext)
            ?: throw DecryptException()
    }

    /** Takes over a successful trial's state. */
    private fun adopt(trial: RatchetSession) {
        dhs = trial.dhs
        dhr = trial.dhr
        rootKey = trial.rootKey
        sendingChainKey = trial.sendingChainKey
        receivingChainKey = trial.receivingChainKey
        sent = trial.sent
        received = trial.received
        previousSent = trial.previousSent
        skipped.clear()
        skipped.putAll(trial.skipped)
        skippedOrder.clear()
        skippedOrder.addAll(trial.skippedOrder)
    }

    private fun sameDhr(dhPublic: ByteArray): Boolean = dhr?.contentEquals(dhPublic) == true

    /** Advances to the peer's new ratchet key: new receiving chain, then sending. */
    private fun dhRatchet(header: RatchetHeader) {
        previousSent = sent
        sent = 0
        received = 0
        dhr = header.dh

        val (rk1, ckr) = kdfRootKey(rootKey, Crypto.diffieHellman(dhs.privateKey, header.dh))
        rootKey = rk1
        receivingChainKey = ckr

        dhs = Crypto.generateKeyPair()
        val (rk2, cks) = kdfRootKey(rootKey, Crypto.diffieHellman(dhs.privateKey, header.dh))
        rootKey = rk2
        sendingChainKey = cks
    }

    /** Stores keys for messages not seen yet, so a late arrival still opens. */
    private fun skipMessageKeys(until: Int) {
        val chain = receivingChainKey ?: return
        val peer = dhr ?: return
        if (until - received > MAX_SKIP) throw DecryptException()

        var current = chain
        while (received < until) {
            val (next, messageKey) = kdfChainKey(current)
            current = next
            storeSkipped(skippedKey(peer, received), messageKey)
            received++
        }
        receivingChainKey = current
    }

    /**
     * Records a message key for a gap, evicting the oldest once the store is
     * full.
     *
     * A bound has to drop something, and the oldest is the right thing to drop:
     * a message that has not arrived after thousands of later ones is not
     * arriving, and being wrong costs one undecryptable message rather than a
     * session.
     */
    private fun storeSkipped(key: String, messageKey: ByteArray) {
        if (!skipped.containsKey(key)) skippedOrder.addLast(key)
        skipped[key] = messageKey
        while (skipped.size > MAX_SKIPPED_KEYS && skippedOrder.isNotEmpty()) {
            skipped.remove(skippedOrder.removeFirst())
        }
    }

    private fun trySkipped(header: RatchetHeader, ciphertext: ByteArray): ByteArray? {
        val key = skippedKey(header.dh, header.n)
        val messageKey = skipped[key] ?: return null
        val plaintext = open(messageKey, RatchetHeaderCodec.marshal(header), ciphertext)
            ?: return null
        // A message key is single-use; keeping it would let a replay succeed.
        // `skippedOrder` keeps its entry: it is an eviction order, not an index,
        // and storeSkipped tolerates naming a key that is already gone.
        skipped.remove(key)
        return plaintext
    }

    fun serialize(): SerializedSession = SerializedSession(
        dhsPrivate = B64.encode(dhs.privateKey),
        dhsPublic = B64.encode(dhs.publicKey),
        dhr = dhr?.let { B64.encode(it) },
        rootKey = B64.encode(rootKey),
        sendingChainKey = sendingChainKey?.let { B64.encode(it) },
        receivingChainKey = receivingChainKey?.let { B64.encode(it) },
        sent = sent,
        received = received,
        previousSent = previousSent,
        skipped = skipped.mapValues { B64.encode(it.value) },
        skippedOrder = skippedOrder.toList(),
    )
}
