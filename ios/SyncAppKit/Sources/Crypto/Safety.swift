import CryptoKit
import Foundation

/// Safety numbers — the out-of-band check that nobody swapped an identity key.
///
/// X3DH authenticates the signed prekey against the identity signing key, so a
/// hostile directory cannot substitute a prekey. Nothing in the protocol stops
/// it substituting the *identity* key itself and serving a different one to each
/// side, which is a textbook MITM: both halves verify perfectly, against the
/// attacker. Comparing a safety number out of band — read aloud, or photographed
/// — is what closes that, because the number is derived from the keys each side
/// actually holds.
///
/// A direct port of `server/pkg/e2e/safety.go`. Every constant is part of the
/// contract: a different iteration count or byte length produces a number that
/// does not match what the other client shows for the same pair of identities,
/// and the failure mode is two people staring at different digits and concluding
/// they are being attacked.
public enum Safety {
    /// Matching Signal. Deliberately slow: it is what makes grinding a chosen
    /// fingerprint expensive.
    private static let iterations = 5200

    /// Bytes of the final digest forming one party's fingerprint (30 → six groups).
    private static let fingerprintBytes = 30

    /// Guards against cross-version fingerprint collisions.
    private static let version = 0

    /// One party's long-term identity, as the number is computed over it.
    public struct Identity: Equatable, Sendable {
        /// Any stable per-user handle both sides agree on — the user id.
        public let stableID: String
        /// X25519 identity public key.
        public let identityKey: Data
        /// Ed25519 identity signing public key.
        public let signingKey: Data

        public init(stableID: String, identityKey: Data, signingKey: Data) {
            self.stableID = stableID
            self.identityKey = identityKey
            self.signingKey = signingKey
        }
    }

    /// The symmetric 60-digit safety number for a conversation between two
    /// identities.
    ///
    /// Both parties compute the identical string regardless of argument order —
    /// they disagree about which side is "local", so the two fingerprints are
    /// combined in a canonical order rather than the caller's. A changed
    /// identity key on either side changes the number, which is the whole signal.
    public static func number(local: Identity, remote: Identity) -> String {
        let a = display(fingerprint(local))
        let b = display(fingerprint(remote))
        return a <= b ? "\(a) \(b)" : "\(b) \(a)"
    }

    /// Reduces one party's identity to a 30-byte digest.
    private static func fingerprint(_ identity: Identity) -> Data {
        // Binding the X25519 agreement key AND the Ed25519 signing key means a
        // MITM has to forge the whole long-term identity, not the half that is
        // checked.
        let key = identity.identityKey + identity.signingKey
        let versionBytes = Data([UInt8((version >> 8) & 0xFF), UInt8(version & 0xFF)])

        var digest = Data(SHA512.hash(data: versionBytes + key + Data(identity.stableID.utf8)))
        // Each round folds the key back in, so the work cannot be precomputed
        // without knowing the key.
        for _ in 0..<iterations {
            digest = Data(SHA512.hash(data: digest + key))
        }
        return digest.prefix(fingerprintBytes)
    }

    /// Renders a fingerprint as six groups of five decimal digits.
    private static func display(_ fingerprint: Data) -> String {
        let bytes = [UInt8](fingerprint)
        var groups: [String] = []
        var i = 0
        while i + 5 <= bytes.count {
            // Five bytes as a big-endian 40-bit integer. A 64-bit accumulator,
            // not 32: 2^40 overflows, and the truncation would produce a
            // plausible-looking number that simply does not match the Go side.
            var value: UInt64 = 0
            for j in 0..<5 { value = (value << 8) | UInt64(bytes[i + j]) }
            // Padded by hand rather than with String(format:). `%05d` reads a
            // 32-bit argument out of a varargs list, and handing it a UInt64 is
            // the kind of mismatch that works on one architecture and not the
            // next.
            let group = String(value % 100_000)
            groups.append(String(repeating: "0", count: max(0, 5 - group.count)) + group)
            i += 5
        }
        return groups.joined(separator: " ")
    }
}
