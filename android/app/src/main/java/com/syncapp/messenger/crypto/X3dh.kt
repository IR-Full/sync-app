package com.syncapp.messenger.crypto

/**
 * X3DH — the asynchronous key agreement that bootstraps a secret chat.
 *
 * A direct port of `server/pkg/e2e/x3dh.go`. Every constant here is part of the
 * wire contract: change the DH order, the 0xFF prefix or the HKDF info string
 * and the two sides silently derive different keys, which surfaces only as
 * "decryption failed" much later.
 */

/** What a device publishes so others can start a session while it is offline. */
data class PreKeyBundle(
    val identityKey: ByteArray,
    val signingKey: ByteArray,
    val signedPreKey: ByteArray,
    val signedPreKeySig: ByteArray,
    /** optional; consumed once by whoever fetched it */
    val oneTimePreKey: ByteArray = ByteArray(0),
) {
    override fun equals(other: Any?): Boolean =
        this === other ||
            (other is PreKeyBundle &&
                identityKey.contentEquals(other.identityKey) &&
                signingKey.contentEquals(other.signingKey) &&
                signedPreKey.contentEquals(other.signedPreKey) &&
                signedPreKeySig.contentEquals(other.signedPreKeySig) &&
                oneTimePreKey.contentEquals(other.oneTimePreKey))

    override fun hashCode(): Int {
        var result = identityKey.contentHashCode()
        result = 31 * result + signingKey.contentHashCode()
        result = 31 * result + signedPreKey.contentHashCode()
        result = 31 * result + signedPreKeySig.contentHashCode()
        return 31 * result + oneTimePreKey.contentHashCode()
    }
}

/** The private keys the initiator holds during X3DH. */
data class InitiatorKeys(val identity: KeyPair, val ephemeral: KeyPair)

/** The private keys the responder holds to complete X3DH. */
data class ResponderKeys(
    val identity: KeyPair,
    val signedPreKey: KeyPair,
    val oneTimePreKey: KeyPair? = null,
)

/** A bundle whose signed prekey is not vouched for by the advertised identity. */
class BadPreKeySignatureException : Exception("e2e: bad signed-prekey signature")

/** The responder was asked to use a one-time prekey it does not hold. */
class MissingOneTimePreKeyException : Exception("e2e: responder missing one-time prekey")

object X3dh {
    private val INFO = "SyncApp-X3DH".toByteArray(Charsets.UTF_8)

    /**
     * Initiator side: derives the shared secret from the peer's published
     * bundle, returning it with the ephemeral public key the responder needs to
     * derive the same secret.
     */
    fun initiator(keys: InitiatorKeys, bundle: PreKeyBundle): Pair<ByteArray, ByteArray> {
        // Verify before trusting anything else in the bundle — unconditionally.
        //
        // Checking only when the bundle carries a signature would hand the
        // attacker the switch: a hostile directory never has to forge anything,
        // it just leaves the fields empty and the handshake proceeds against a
        // prekey nobody vouched for. An unsigned bundle and a substituted one
        // are indistinguishable from here, so both are rejected.
        if (!Crypto.verifyPreKey(bundle.signingKey, bundle.signedPreKey, bundle.signedPreKeySig)) {
            throw BadPreKeySignatureException()
        }

        // DH1 = DH(IK_A, SPK_B); DH2 = DH(EK_A, IK_B); DH3 = DH(EK_A, SPK_B)
        val dh1 = Crypto.diffieHellman(keys.identity.privateKey, bundle.signedPreKey)
        val dh2 = Crypto.diffieHellman(keys.ephemeral.privateKey, bundle.identityKey)
        val dh3 = Crypto.diffieHellman(keys.ephemeral.privateKey, bundle.signedPreKey)
        var concat = dh1 + dh2 + dh3

        // DH4 = DH(EK_A, OPK_B), only when the bundle carried a one-time prekey.
        if (bundle.oneTimePreKey.isNotEmpty()) {
            concat += Crypto.diffieHellman(keys.ephemeral.privateKey, bundle.oneTimePreKey)
        }

        return rootFromDH(concat) to keys.ephemeral.publicKey
    }

    /**
     * Responder side: derives the same shared secret from the initiator's
     * identity and ephemeral public keys.
     */
    fun responder(
        keys: ResponderKeys,
        initiatorIdentity: ByteArray,
        initiatorEphemeral: ByteArray,
        usedOneTime: Boolean,
    ): ByteArray {
        // Mirror of the initiator: DH1 = DH(SPK_B, IK_A); DH2 = DH(IK_B, EK_A);
        // DH3 = DH(SPK_B, EK_A).
        val dh1 = Crypto.diffieHellman(keys.signedPreKey.privateKey, initiatorIdentity)
        val dh2 = Crypto.diffieHellman(keys.identity.privateKey, initiatorEphemeral)
        val dh3 = Crypto.diffieHellman(keys.signedPreKey.privateKey, initiatorEphemeral)
        var concat = dh1 + dh2 + dh3

        if (usedOneTime) {
            val otp = keys.oneTimePreKey ?: throw MissingOneTimePreKeyException()
            concat += Crypto.diffieHellman(otp.privateKey, initiatorEphemeral)
        }
        return rootFromDH(concat)
    }

    /** Turns the concatenated DH outputs into the 32-byte root key. */
    private fun rootFromDH(dhConcat: ByteArray): ByteArray {
        // The 32-byte 0xFF prefix is the X3DH spec's domain separator.
        val prefix = ByteArray(32) { 0xFF.toByte() }
        return Crypto.hkdf(prefix + dhConcat, null, INFO, 32)
    }
}
