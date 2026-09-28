package com.syncapp.messenger.crypto

import org.bouncycastle.crypto.agreement.X25519Agreement
import org.bouncycastle.crypto.digests.SHA256Digest
import org.bouncycastle.crypto.generators.HKDFBytesGenerator
import org.bouncycastle.crypto.macs.HMac
import org.bouncycastle.crypto.params.Ed25519PrivateKeyParameters
import org.bouncycastle.crypto.params.Ed25519PublicKeyParameters
import org.bouncycastle.crypto.params.HKDFParameters
import org.bouncycastle.crypto.params.KeyParameter
import org.bouncycastle.crypto.params.X25519PrivateKeyParameters
import org.bouncycastle.crypto.params.X25519PublicKeyParameters
import org.bouncycastle.crypto.signers.Ed25519Signer
import java.security.SecureRandom
import java.util.Base64

/**
 * Key primitives for secret chats, matching `server/pkg/e2e`:
 * X25519 for agreement, Ed25519 for prekey signatures.
 *
 * Every constant and byte order here is part of the wire contract. The Go
 * server, the TypeScript web client and this file must agree exactly; where they
 * do not, the only symptom is "decryption failed" long after the mistake, with
 * nothing pointing at serialisation as the cause. `CryptoInteropTest` pins the
 * agreement against vectors produced by the Go implementation.
 */

/** 32-byte X25519 key pair. */
data class KeyPair(val privateKey: ByteArray, val publicKey: ByteArray) {
    // Data classes compare arrays by identity, which would make two equal keys
    // unequal — and silently break any map or assertion keyed on one.
    override fun equals(other: Any?): Boolean =
        this === other ||
            (other is KeyPair &&
                privateKey.contentEquals(other.privateKey) &&
                publicKey.contentEquals(other.publicKey))

    override fun hashCode(): Int = 31 * privateKey.contentHashCode() + publicKey.contentHashCode()
}

/**
 * Ed25519 signing key pair.
 *
 * `privateKey` is the 32-byte seed. Go stores a 64-byte private key (seed plus
 * public half); the seed is the part that actually needs keeping, and both
 * libraries derive the rest from it.
 */
data class SigningKeyPair(val privateKey: ByteArray, val publicKey: ByteArray) {
    override fun equals(other: Any?): Boolean =
        this === other ||
            (other is SigningKeyPair &&
                privateKey.contentEquals(other.privateKey) &&
                publicKey.contentEquals(other.publicKey))

    override fun hashCode(): Int = 31 * privateKey.contentHashCode() + publicKey.contentHashCode()
}

object Crypto {
    /**
     * One shared CSPRNG.
     *
     * `SecureRandom()` on Android is seeded by the OS; constructing a fresh one
     * per key would add nothing but the cost of reseeding.
     */
    private val random = SecureRandom()

    const val KEY_LEN = X25519PrivateKeyParameters.KEY_SIZE
    const val SIGNING_KEY_LEN = Ed25519PrivateKeyParameters.KEY_SIZE
    const val SIGNATURE_LEN = Ed25519PrivateKeyParameters.SIGNATURE_SIZE

    fun generateKeyPair(): KeyPair {
        val priv = X25519PrivateKeyParameters(random)
        return KeyPair(priv.encoded, priv.generatePublicKey().encoded)
    }

    fun generateSigningKeyPair(): SigningKeyPair {
        val priv = Ed25519PrivateKeyParameters(random)
        return SigningKeyPair(priv.encoded, priv.generatePublicKey().encoded)
    }

    /** X25519 Diffie-Hellman — the `dh()` helper on the Go side. */
    fun diffieHellman(privateKey: ByteArray, publicKey: ByteArray): ByteArray {
        val agreement = X25519Agreement()
        agreement.init(X25519PrivateKeyParameters(privateKey, 0))
        val out = ByteArray(agreement.agreementSize)
        agreement.calculateAgreement(X25519PublicKeyParameters(publicKey, 0), out, 0)
        return out
    }

    /** Signs a signed-prekey's public bytes, as `e2e.SignPreKey` does. */
    fun signPreKey(signingPrivateKey: ByteArray, signedPreKeyPublic: ByteArray): ByteArray {
        val signer = Ed25519Signer()
        signer.init(true, Ed25519PrivateKeyParameters(signingPrivateKey, 0))
        signer.update(signedPreKeyPublic, 0, signedPreKeyPublic.size)
        return signer.generateSignature()
    }

    /**
     * Verifies a bundle's prekey signature.
     *
     * This is the MITM defence: without it a hostile key directory could hand
     * out a prekey it controls. A bundle that fails here must be rejected, not
     * "tried anyway" — and the length checks are part of that, because a bundle
     * with no signature at all must fail rather than skip the check.
     */
    fun verifyPreKey(
        signingPublicKey: ByteArray,
        signedPreKeyPublic: ByteArray,
        signature: ByteArray,
    ): Boolean {
        if (signingPublicKey.size != KEY_LEN || signature.size != SIGNATURE_LEN) return false
        return runCatching {
            val verifier = Ed25519Signer()
            verifier.init(false, Ed25519PublicKeyParameters(signingPublicKey, 0))
            verifier.update(signedPreKeyPublic, 0, signedPreKeyPublic.size)
            verifier.verifySignature(signature)
        }.getOrDefault(false)
    }

    /** HKDF-SHA256. `salt` may be null, which HKDF treats as a zero-filled block. */
    fun hkdf(ikm: ByteArray, salt: ByteArray?, info: ByteArray, length: Int): ByteArray {
        val generator = HKDFBytesGenerator(SHA256Digest())
        generator.init(HKDFParameters(ikm, salt, info))
        val out = ByteArray(length)
        generator.generateBytes(out, 0, length)
        return out
    }

    /** HMAC-SHA256. */
    fun hmacSha256(key: ByteArray, message: ByteArray): ByteArray {
        val mac = HMac(SHA256Digest())
        mac.init(KeyParameter(key))
        mac.update(message, 0, message.size)
        val out = ByteArray(mac.macSize)
        mac.doFinal(out, 0)
        return out
    }

    /**
     * Constant-time comparison.
     *
     * Used on public keys, where a timing signal is not a key compromise — but
     * it does leak which prefix of a forged key is correct, which is a free hint
     * to anyone grinding one.
     */
    fun constantTimeEquals(a: ByteArray, b: ByteArray): Boolean {
        if (a.size != b.size) return false
        var diff = 0
        for (i in a.indices) diff = diff or (a[i].toInt() xor b[i].toInt())
        return diff == 0
    }
}

/**
 * Base64 with padding, matching Go's `base64.StdEncoding`.
 *
 * NOT url-safe: the gateway decodes prekeys with StdEncoding, and a url-safe
 * alphabet produces keys it silently rejects as malformed.
 *
 * `java.util.Base64` rather than `android.util.Base64`, which is the obvious
 * choice here and the wrong one. The platform class would make this whole
 * package depend on the Android framework, so every test of the ratchet — pure
 * arithmetic over byte arrays — would need Robolectric and a matching SDK
 * level. The java.util version is available from API 26, which is this app's
 * minSdk, and emits the padded standard alphabet the server expects.
 */
object B64 {
    private val encoder: Base64.Encoder = Base64.getEncoder()
    private val decoder: Base64.Decoder = Base64.getDecoder()

    fun encode(bytes: ByteArray): String = encoder.encodeToString(bytes)

    /** Returns null for anything that is not valid base64, so callers can reject. */
    fun decodeOrNull(value: String): ByteArray? =
        runCatching { decoder.decode(value) }.getOrNull()
}
