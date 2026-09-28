package com.syncapp.messenger.crypto

import org.bouncycastle.crypto.digests.SHA512Digest

/**
 * Safety numbers — the out-of-band check that nobody swapped an identity key.
 *
 * X3DH authenticates the signed prekey against the identity signing key, so a
 * hostile directory cannot substitute a prekey. Nothing in the protocol stops it
 * substituting the *identity* key itself and serving a different one to each
 * side, which is a textbook MITM: both halves verify perfectly, against the
 * attacker. Comparing a safety number out of band — read aloud, or photographed
 * — is what closes that, because the number is derived from the keys each side
 * actually holds.
 *
 * A direct port of `server/pkg/e2e/safety.go`. Every constant is part of the
 * contract: a different iteration count or byte length produces a number that
 * does not match what the other client shows for the same pair of identities,
 * and the failure mode is two people staring at different digits and concluding
 * they are being attacked.
 */
object Safety {
    /** Matching Signal. Deliberately slow: it is what makes grinding a chosen
     * fingerprint expensive. */
    private const val ITERATIONS = 5200

    /** Bytes of the final digest forming one party's fingerprint (30 → six groups). */
    private const val FINGERPRINT_BYTES = 30

    /** Guards against cross-version fingerprint collisions. */
    private const val VERSION = 0

    /** One party's long-term identity, as the number is computed over it. */
    data class Identity(
        /** any stable per-user handle both sides agree on — the user id */
        val stableId: String,
        /** X25519 identity public key */
        val identityKey: ByteArray,
        /** Ed25519 identity signing public key */
        val signingKey: ByteArray,
    ) {
        override fun equals(other: Any?): Boolean =
            this === other ||
                (other is Identity &&
                    stableId == other.stableId &&
                    identityKey.contentEquals(other.identityKey) &&
                    signingKey.contentEquals(other.signingKey))

        override fun hashCode(): Int {
            var result = stableId.hashCode()
            result = 31 * result + identityKey.contentHashCode()
            return 31 * result + signingKey.contentHashCode()
        }
    }

    /**
     * The symmetric 60-digit safety number for a conversation between two
     * identities.
     *
     * Both parties compute the identical string regardless of argument order —
     * they disagree about which side is "local", so the two fingerprints are
     * combined in a canonical order rather than the caller's. A changed identity
     * key on either side changes the number, which is the whole signal.
     */
    fun number(local: Identity, remote: Identity): String {
        val a = display(fingerprint(local))
        val b = display(fingerprint(remote))
        return if (a <= b) "$a $b" else "$b $a"
    }

    /** Reduces one party's identity to a 30-byte digest. */
    private fun fingerprint(identity: Identity): ByteArray {
        // Binding the X25519 agreement key AND the Ed25519 signing key means a
        // MITM has to forge the whole long-term identity, not the half that is
        // checked.
        val key = identity.identityKey + identity.signingKey
        val version = byteArrayOf(((VERSION shr 8) and 0xFF).toByte(), (VERSION and 0xFF).toByte())

        var digest = sha512(version + key + identity.stableId.toByteArray(Charsets.UTF_8))
        // Each round folds the key back in, so the work cannot be precomputed
        // without knowing the key.
        repeat(ITERATIONS) { digest = sha512(digest + key) }
        return digest.copyOf(FINGERPRINT_BYTES)
    }

    /** Renders a fingerprint as six groups of five decimal digits. */
    private fun display(fp: ByteArray): String {
        val groups = mutableListOf<String>()
        var i = 0
        while (i + 5 <= fp.size) {
            // Five bytes as a big-endian 40-bit integer. A Long, not an Int:
            // 2^40 overflows 32 bits, and the truncation would produce a
            // plausible-looking number that simply does not match the Go side.
            var value = 0L
            for (j in 0 until 5) value = (value shl 8) or (fp[i + j].toLong() and 0xFF)
            groups += (value % 100000L).toString().padStart(5, '0')
            i += 5
        }
        return groups.joinToString(" ")
    }

    private fun sha512(input: ByteArray): ByteArray {
        val digest = SHA512Digest()
        digest.update(input, 0, input.size)
        val out = ByteArray(digest.digestSize)
        digest.doFinal(out, 0)
        return out
    }
}
