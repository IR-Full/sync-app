import Foundation
import SyncAppCrypto
import SyncAppDomain

/// Safety numbers and identity pins, over the secret-chat service.
public final class SecretSafetyRepositoryImpl: SecretSafetyRepository, @unchecked Sendable {
    private let secret: SecretChatService
    private let sync: SyncEngine

    public init(secret: SecretChatService, sync: SyncEngine) {
        self.secret = secret
        self.sync = sync
    }

    public func devices(peerUserID: String) async throws -> [DeviceSafety] {
        let ourUserID = await sync.userID
        return try await ErrorMapping.mapped {
            try await secret.safety(peerUserID: peerUserID, ourUserID: ourUserID).map(Self.device)
        }
    }

    public func accept(_ device: DeviceSafety) async throws {
        try await ErrorMapping.mapped {
            try await secret.acceptIdentity(
                userID: device.userID,
                deviceID: device.deviceID,
                identityKey: device.identityKey,
                signingKey: device.signingKey
            )
        }
    }

    static func device(_ info: SecretChatService.DeviceSafetyInfo) -> DeviceSafety {
        let status: IdentityStatus
        switch info.verdict {
        case .firstUse: status = .firstUse
        case .known: status = .known
        case .changed: status = .changed
        }
        return DeviceSafety(
            userID: info.userID,
            deviceID: info.deviceID,
            safetyNumber: info.number,
            status: status,
            identityKey: info.identityKey,
            signingKey: info.signingKey
        )
    }
}
