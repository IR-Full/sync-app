import Foundation

/// The stable per-installation device id sent in `HELLO`.
///
/// The gateway keys sessions, multi-device delivery and push tokens on this,
/// so it must survive app launches.
public enum DeviceIdentity {
    private static let account = "chat.synapse.device-id"

    public static func current(
        keychain: KeychainStore = KeychainStore()
    ) -> String {
        if let existing = try? keychain.string(forKey: account),
           !existing.isEmpty {
            return existing
        }

        let generated = UUID().uuidString
        try? keychain.set(generated, forKey: account)

        return generated
    }
}