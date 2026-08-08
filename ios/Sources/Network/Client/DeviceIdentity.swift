import Foundation

/// The stable per-installation device id sent in `HELLO`.
///
/// The gateway keys sessions, multi-device delivery and push tokens on this, so
/// it must survive app launches — a new id on every launch would look like a new
/// device to the server and quietly fan out every message to a growing list of
/// ghosts.
///
/// It is *not* `identifierForVendor`: that value changes when the user deletes
/// every app from the vendor, and it is shared across our own apps. A UUID we
/// mint once and keep in the keychain is both stable and ours. The keychain
/// (rather than `UserDefaults`) is what makes it survive an app reinstall, so a
/// reinstall resumes the same device rather than orphaning its push token.
public enum DeviceIdentity {
    private static let account = "chat.synapse.device-id"

    public static func current(keychain: KeychainStore = KeychainStore()) -> String {
        if let existing = try? keychain.string(forKey: account), let existing, !existing.isEmpty {
            return existing
        }
        let generated = UUID().uuidString
        try? keychain.set(generated, forKey: account)
        return generated
    }
}
