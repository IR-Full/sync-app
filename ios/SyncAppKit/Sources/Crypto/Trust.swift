import Foundation

/// Trust-on-first-use pinning of peer identity keys.
///
/// A safety number lets two people DETECT a swapped identity key — if they think
/// to compare one. Almost nobody does. Pinning is the half that does not depend
/// on anyone remembering: the first time a peer device is seen its identity keys
/// are recorded, and every later session with that device is checked against the
/// record.
///
/// Its limits are worth stating rather than implying. It cannot protect a first
/// contact: a directory that lies from the very beginning is believed. What it
/// does is turn the attack window from "every session, forever" into "one
/// moment" — a server that later starts handing out its own keys is caught
/// immediately, on every existing conversation at once, which is the realistic
/// shape of the threat.
///
/// And a changed key is NOT automatically an attack. Reinstalls happen, devices
/// get wiped, people switch phones. So this reports and the human decides; it
/// does not block on its own. A port of `server/pkg/e2e/trust.go`.
public struct PinnedIdentity: Codable, Equatable, Sendable {
    public let userID: String
    public let deviceID: String
    /// Base64 X25519 identity public key.
    public let identityKey: String
    /// Base64 Ed25519 identity signing public key.
    public let signingKey: String
    /// Unix millis, matching the other clients' stored pins.
    public let firstSeen: Int64

    public init(
        userID: String,
        deviceID: String,
        identityKey: String,
        signingKey: String,
        firstSeen: Int64
    ) {
        self.userID = userID
        self.deviceID = deviceID
        self.identityKey = identityKey
        self.signingKey = signingKey
        self.firstSeen = firstSeen
    }
}

/// What a verification found.
public enum TrustVerdict: Equatable, Sendable {
    /// Never seen before — pin after the session is established.
    case firstUse
    /// The keys match what was pinned.
    case known
    /// The keys differ from the pin. The caller must NOT proceed silently: this
    /// is the moment the safety number is worth showing.
    case changed(PinnedIdentity)
}

/// The pin store.
///
/// Pure state plus lookups, with persistence left to the caller — the same shape
/// as the ratchet session. That keeps this testable without a device and lets
/// the storage layer decide when a write is worth making.
public final class TrustStore {
    private var pins: [String: PinnedIdentity]

    /// Injectable so a test can pin a known instant instead of "now".
    private let now: () -> Int64

    public init(
        pins: [String: PinnedIdentity] = [:],
        now: @escaping () -> Int64 = { Int64(Date().timeIntervalSince1970 * 1000) }
    ) {
        self.pins = pins
        self.now = now
    }

    public func snapshot() -> [String: PinnedIdentity] { pins }

    public func verify(
        userID: String,
        deviceID: String,
        identityKey: String,
        signingKey: String
    ) -> TrustVerdict {
        guard let pinned = pins[Self.key(userID, deviceID)] else { return .firstUse }
        let matches = equalKeys(pinned.identityKey, identityKey)
            && equalKeys(pinned.signingKey, signingKey)
        return matches ? .known : .changed(pinned)
    }

    /// Records or replaces a pin. Replacing one is an explicit act: it should
    /// follow a human confirming the change, not a client deciding on its own
    /// that the new key is fine.
    public func accept(
        userID: String,
        deviceID: String,
        identityKey: String,
        signingKey: String
    ) {
        pins[Self.key(userID, deviceID)] = PinnedIdentity(
            userID: userID,
            deviceID: deviceID,
            identityKey: identityKey,
            signingKey: signingKey,
            firstSeen: now()
        )
    }

    public func forget(userID: String, deviceID: String) {
        pins.removeValue(forKey: Self.key(userID, deviceID))
    }

    private static func key(_ userID: String, _ deviceID: String) -> String {
        "\(userID):\(deviceID)"
    }

    /// Constant-time comparison of two base64 keys.
    ///
    /// These are public keys, so a timing leak is not a key compromise — but it
    /// still tells an attacker which prefix of a forged key is correct, which is
    /// a free hint to anyone grinding one. Anything that does not decode counts
    /// as a mismatch: a malformed value must never read as a match.
    private func equalKeys(_ a: String, _ b: String) -> Bool {
        guard let left = B64.decode(a), let right = B64.decode(b) else { return false }
        return Crypto.constantTimeEquals(left, right)
    }
}
