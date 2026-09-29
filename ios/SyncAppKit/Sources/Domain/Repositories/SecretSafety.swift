import Foundation

/// What this device knows about a peer device's identity key.
public enum IdentityStatus: Equatable, Sendable {
    /// Never pinned: no secret message has gone to this device yet, or the pin was
    /// replaced. Comparing the safety number now is what makes the pin worth anything.
    case firstUse
    /// Matches the key pinned the first time, or the one a person accepted.
    case known
    /// Differs from the pinned key. Sending to this peer stops until someone looks at
    /// the safety number and accepts the new key.
    case changed
}

/// One peer device, as the safety screen shows it.
public struct DeviceSafety: Identifiable, Equatable, Sendable {
    public var id: String { "\(userID):\(deviceID)" }
    public let userID: String
    public let deviceID: String
    /// Sixty digits in twelve groups; identical on both ends when no key was swapped.
    public let safetyNumber: String
    public let status: IdentityStatus
    /// The keys the number was computed from. Accepting pins exactly these, so what is
    /// pinned is what was displayed rather than whatever a second fetch returns.
    public let identityKey: String
    public let signingKey: String

    public init(
        userID: String,
        deviceID: String,
        safetyNumber: String,
        status: IdentityStatus,
        identityKey: String,
        signingKey: String
    ) {
        self.userID = userID
        self.deviceID = deviceID
        self.safetyNumber = safetyNumber
        self.status = status
        self.identityKey = identityKey
        self.signingKey = signingKey
    }
}

/// Safety numbers and identity pinning for secret chats.
public protocol SecretSafetyRepository: Sendable {
    /// Every device the peer has published keys for, with its safety number.
    func devices(peerUserID: String) async throws -> [DeviceSafety]
    /// Pins the keys shown for `device`, replacing a changed pin.
    func accept(_ device: DeviceSafety) async throws
}
