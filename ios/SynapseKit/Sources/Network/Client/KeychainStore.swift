import Foundation
import Security

/// Keychain-backed storage for the two things that must never sit in
/// `UserDefaults`: the bearer token and the resume token.
///
/// The distinction matters. `UserDefaults` is a plist in the app container —
/// readable from a file-system dump of an unlocked, jailbroken, or backed-up
/// device. A session token there is a credential in plaintext. Theme and
/// language preferences belong in `UserDefaults`; a token that authenticates as
/// the user does not.
///
/// `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly` is deliberate:
/// *afterFirstUnlock* so a push-triggered background launch can still reconnect
/// on a locked phone, and *thisDeviceOnly* so the token never rides an encrypted
/// backup onto a different device.
public struct KeychainStore: Sendable {
    private let service: String

    public init(service: String = "chat.synapse.ios") {
        self.service = service
    }

    public enum KeychainError: Error, Equatable, Sendable {
        case unexpectedStatus(OSStatus)
    }

    public func set(_ value: String, forKey key: String) throws {
        let data = Data(value.utf8)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
        ]
        let attributes: [String: Any] = [
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]

        let status = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        switch status {
        case errSecSuccess:
            return
        case errSecItemNotFound:
            var insert = query
            insert.merge(attributes) { current, _ in current }
            let addStatus = SecItemAdd(insert as CFDictionary, nil)
            guard addStatus == errSecSuccess else { throw KeychainError.unexpectedStatus(addStatus) }
        default:
            throw KeychainError.unexpectedStatus(status)
        }
    }

    public func string(forKey key: String) throws -> String? {
        guard let data = try data(forKey: key) else { return nil }
        return String(data: data, encoding: .utf8)
    }

    public func data(forKey key: String) throws -> Data? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        switch status {
        case errSecSuccess:
            return result as? Data
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError.unexpectedStatus(status)
        }
    }

    public func remove(key: String) throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
        ]
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.unexpectedStatus(status)
        }
    }

    // MARK: - Codable convenience

    public func set<T: Encodable>(_ value: T, forKey key: String) throws {
        let data = try JSONEncoder().encode(value)
        guard let string = String(data: data, encoding: .utf8) else { return }
        try set(string, forKey: key)
    }

    public func value<T: Decodable>(_ type: T.Type, forKey key: String) throws -> T? {
        guard let data = try data(forKey: key) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
}
