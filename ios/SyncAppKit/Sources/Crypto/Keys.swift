import CryptoKit
import Foundation

/// Key primitives for secret chats, matching `server/pkg/e2e`:
/// X25519 for agreement, Ed25519 for prekey signatures.
///
/// Every constant and byte order here is part of the wire contract. The Go
/// server, the TypeScript web client, the Kotlin client and this file must agree
/// exactly; where they do not, the only symptom is "decryption failed" long
/// after the mistake, with nothing pointing at serialisation as the cause.
///
/// CryptoKit rather than a dependency: the package has none by policy, and every
/// primitive this needs is in the system framework. It also means the private
/// keys live in `CryptoKit`'s own types where they can, though the ratchet
/// forces raw bytes — see `RatchetSession`.

/// 32-byte X25519 key pair.
public struct SecretKeyPair: Equatable, Sendable {
    public let privateKey: Data
    public let publicKey: Data

    public init(privateKey: Data, publicKey: Data) {
        self.privateKey = privateKey
        self.publicKey = publicKey
    }
}

/// Ed25519 signing key pair.
///
/// `privateKey` is the 32-byte seed. Go stores a 64-byte private key (seed plus
/// public half); the seed is the part that actually needs keeping, and every
/// implementation derives the rest from it.
public struct SigningKeyPair: Equatable, Sendable {
    public let privateKey: Data
    public let publicKey: Data

    public init(privateKey: Data, publicKey: Data) {
        self.privateKey = privateKey
        self.publicKey = publicKey
    }
}

public enum Crypto {
    public static let keyLength = 32
    public static let signatureLength = 64

    public static func generateKeyPair() -> SecretKeyPair {
        let priv = Curve25519.KeyAgreement.PrivateKey()
        return SecretKeyPair(
            privateKey: priv.rawRepresentation,
            publicKey: priv.publicKey.rawRepresentation
        )
    }

    public static func generateSigningKeyPair() -> SigningKeyPair {
        let priv = Curve25519.Signing.PrivateKey()
        return SigningKeyPair(
            privateKey: priv.rawRepresentation,
            publicKey: priv.publicKey.rawRepresentation
        )
    }

    /// X25519 Diffie-Hellman — the `dh()` helper on the Go side.
    ///
    /// Returns nil for malformed key material rather than throwing: every caller
    /// is already in a position where the answer is "this bundle is not usable",
    /// and the distinction between "wrong length" and "not on the curve" is not
    /// one any of them acts on differently.
    public static func diffieHellman(privateKey: Data, publicKey: Data) -> Data? {
        guard
            let priv = try? Curve25519.KeyAgreement.PrivateKey(rawRepresentation: privateKey),
            let pub = try? Curve25519.KeyAgreement.PublicKey(rawRepresentation: publicKey),
            let shared = try? priv.sharedSecretFromKeyAgreement(with: pub)
        else { return nil }

        // CryptoKit hides the raw agreement behind SharedSecret to discourage
        // using it as a key directly. X3DH genuinely needs the bytes: they are
        // concatenated with three other agreements and run through one HKDF, so
        // deriving here would be the wrong shape and a different secret from
        // every other implementation.
        return shared.withUnsafeBytes { Data($0) }
    }

    /// Signs a signed-prekey's public bytes, as `e2e.SignPreKey` does.
    public static func signPreKey(signingPrivateKey: Data, signedPreKeyPublic: Data) -> Data? {
        guard
            let priv = try? Curve25519.Signing.PrivateKey(rawRepresentation: signingPrivateKey),
            let signature = try? priv.signature(for: signedPreKeyPublic)
        else { return nil }
        return signature
    }

    /// Verifies a bundle's prekey signature.
    ///
    /// This is the MITM defence: without it a hostile key directory could hand
    /// out a prekey it controls. A bundle that fails here must be rejected, not
    /// "tried anyway" — and the length checks are part of that, because a bundle
    /// with no signature at all must fail rather than skip the check.
    public static func verifyPreKey(
        signingPublicKey: Data,
        signedPreKeyPublic: Data,
        signature: Data
    ) -> Bool {
        guard signingPublicKey.count == keyLength, signature.count == signatureLength else {
            return false
        }
        guard let pub = try? Curve25519.Signing.PublicKey(rawRepresentation: signingPublicKey) else {
            return false
        }
        return pub.isValidSignature(signature, for: signedPreKeyPublic)
    }

    /// HKDF-SHA256. A nil `salt` is RFC 5869's "no salt", a zero-filled block.
    public static func hkdf(ikm: Data, salt: Data?, info: Data, length: Int) -> Data {
        let key = HKDF<SHA256>.deriveKey(
            inputKeyMaterial: SymmetricKey(data: ikm),
            salt: salt ?? Data(),
            info: info,
            outputByteCount: length
        )
        return key.withUnsafeBytes { Data($0) }
    }

    /// HMAC-SHA256.
    public static func hmacSHA256(key: Data, message: Data) -> Data {
        Data(HMAC<SHA256>.authenticationCode(for: message, using: SymmetricKey(data: key)))
    }

    /// Constant-time comparison.
    ///
    /// Used on public keys, where a timing signal is not a key compromise — but
    /// it does leak which prefix of a forged key is correct, which is a free
    /// hint to anyone grinding one.
    public static func constantTimeEquals(_ a: Data, _ b: Data) -> Bool {
        guard a.count == b.count else { return false }
        var diff: UInt8 = 0
        for (x, y) in zip(a, b) { diff |= x ^ y }
        return diff == 0
    }
}

/// Base64 with padding, matching Go's `base64.StdEncoding`.
///
/// NOT url-safe: the gateway decodes prekeys with StdEncoding, and a url-safe
/// alphabet produces keys it silently rejects as malformed.
public enum B64 {
    public static func encode(_ data: Data) -> String {
        data.base64EncodedString()
    }

    /// Returns nil for anything that is not valid base64, so callers can reject.
    ///
    /// `ignoreUnknownCharacters` is deliberately NOT set: with it, a corrupted
    /// key decodes to a shorter one instead of failing, and the failure then
    /// surfaces as an unopenable message rather than a bad bundle.
    public static func decode(_ value: String) -> Data? {
        Data(base64Encoded: value)
    }
}
