import Foundation

/// X3DH — the asynchronous key agreement that bootstraps a secret chat.
///
/// A direct port of `server/pkg/e2e/x3dh.go`. Every constant here is part of the
/// wire contract: change the DH order, the 0xFF prefix or the HKDF info string
/// and the two sides silently derive different keys, which surfaces only as
/// "decryption failed" much later.

/// What a device publishes so others can start a session while it is offline.
public struct PreKeyBundle: Equatable, Sendable {
    public let identityKey: Data
    public let signingKey: Data
    public let signedPreKey: Data
    public let signedPreKeySignature: Data
    /// Optional; consumed once by whoever fetched it.
    public let oneTimePreKey: Data

    public init(
        identityKey: Data,
        signingKey: Data,
        signedPreKey: Data,
        signedPreKeySignature: Data,
        oneTimePreKey: Data = Data()
    ) {
        self.identityKey = identityKey
        self.signingKey = signingKey
        self.signedPreKey = signedPreKey
        self.signedPreKeySignature = signedPreKeySignature
        self.oneTimePreKey = oneTimePreKey
    }
}

/// The private keys the initiator holds during X3DH.
public struct InitiatorKeys: Sendable {
    public let identity: SecretKeyPair
    public let ephemeral: SecretKeyPair

    public init(identity: SecretKeyPair, ephemeral: SecretKeyPair) {
        self.identity = identity
        self.ephemeral = ephemeral
    }
}

/// The private keys the responder holds to complete X3DH.
public struct ResponderKeys: Sendable {
    public let identity: SecretKeyPair
    public let signedPreKey: SecretKeyPair
    public let oneTimePreKey: SecretKeyPair?

    public init(
        identity: SecretKeyPair,
        signedPreKey: SecretKeyPair,
        oneTimePreKey: SecretKeyPair? = nil
    ) {
        self.identity = identity
        self.signedPreKey = signedPreKey
        self.oneTimePreKey = oneTimePreKey
    }
}

public enum X3DHError: Error, Equatable, Sendable {
    /// The signed prekey is not vouched for by the advertised identity.
    case badPreKeySignature
    /// The responder was asked to use a one-time prekey it does not hold.
    case missingOneTimePreKey
    /// A key in the bundle is not usable X25519 material.
    case badKeyMaterial
}

public enum X3DH {
    private static let info = Data("SyncApp-X3DH".utf8)

    /// Initiator side: derives the shared secret from the peer's published
    /// bundle, returning it with the ephemeral public key the responder needs to
    /// derive the same secret.
    public static func initiator(
        keys: InitiatorKeys,
        bundle: PreKeyBundle
    ) throws -> (sharedSecret: Data, ephemeralPublicKey: Data) {
        // Verify before trusting anything else in the bundle — unconditionally.
        //
        // Checking only when the bundle carries a signature would hand the
        // attacker the switch: a hostile directory never has to forge anything,
        // it just leaves the fields empty and the handshake proceeds against a
        // prekey nobody vouched for. An unsigned bundle and a substituted one
        // are indistinguishable from here, so both are rejected.
        guard Crypto.verifyPreKey(
            signingPublicKey: bundle.signingKey,
            signedPreKeyPublic: bundle.signedPreKey,
            signature: bundle.signedPreKeySignature
        ) else {
            throw X3DHError.badPreKeySignature
        }

        // DH1 = DH(IK_A, SPK_B); DH2 = DH(EK_A, IK_B); DH3 = DH(EK_A, SPK_B)
        guard
            let dh1 = Crypto.diffieHellman(
                privateKey: keys.identity.privateKey, publicKey: bundle.signedPreKey),
            let dh2 = Crypto.diffieHellman(
                privateKey: keys.ephemeral.privateKey, publicKey: bundle.identityKey),
            let dh3 = Crypto.diffieHellman(
                privateKey: keys.ephemeral.privateKey, publicKey: bundle.signedPreKey)
        else { throw X3DHError.badKeyMaterial }

        var concat = dh1 + dh2 + dh3

        // DH4 = DH(EK_A, OPK_B), only when the bundle carried a one-time prekey.
        if !bundle.oneTimePreKey.isEmpty {
            guard let dh4 = Crypto.diffieHellman(
                privateKey: keys.ephemeral.privateKey, publicKey: bundle.oneTimePreKey)
            else { throw X3DHError.badKeyMaterial }
            concat += dh4
        }

        return (rootFromDH(concat), keys.ephemeral.publicKey)
    }

    /// Responder side: derives the same shared secret from the initiator's
    /// identity and ephemeral public keys.
    public static func responder(
        keys: ResponderKeys,
        initiatorIdentity: Data,
        initiatorEphemeral: Data,
        usedOneTime: Bool
    ) throws -> Data {
        // Mirror of the initiator: DH1 = DH(SPK_B, IK_A); DH2 = DH(IK_B, EK_A);
        // DH3 = DH(SPK_B, EK_A).
        guard
            let dh1 = Crypto.diffieHellman(
                privateKey: keys.signedPreKey.privateKey, publicKey: initiatorIdentity),
            let dh2 = Crypto.diffieHellman(
                privateKey: keys.identity.privateKey, publicKey: initiatorEphemeral),
            let dh3 = Crypto.diffieHellman(
                privateKey: keys.signedPreKey.privateKey, publicKey: initiatorEphemeral)
        else { throw X3DHError.badKeyMaterial }

        var concat = dh1 + dh2 + dh3

        if usedOneTime {
            guard let otp = keys.oneTimePreKey else { throw X3DHError.missingOneTimePreKey }
            guard let dh4 = Crypto.diffieHellman(
                privateKey: otp.privateKey, publicKey: initiatorEphemeral)
            else { throw X3DHError.badKeyMaterial }
            concat += dh4
        }

        return rootFromDH(concat)
    }

    /// Turns the concatenated DH outputs into the 32-byte root key.
    private static func rootFromDH(_ dhConcat: Data) -> Data {
        // The 32-byte 0xFF prefix is the X3DH spec's domain separator.
        let prefix = Data(repeating: 0xFF, count: 32)
        return Crypto.hkdf(ikm: prefix + dhConcat, salt: nil, info: info, length: 32)
    }
}
