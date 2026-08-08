import Foundation
import SynapseDomain
import SynapseNetwork

/// Sessions, and the two credentials worth persisting.
///
/// The bearer token and the resume token go to the keychain, never to
/// `UserDefaults`: they authenticate as the user, and a plist in the app
/// container is not a place to keep that. The account record (ids, the username
/// the person typed) is not a credential and lives in the cache.
public final class AuthRepositoryImpl: AuthRepository, @unchecked Sendable {
    private let client: SynapseClient
    private let store: LocalStore
    private let sync: SyncEngine
    private let keychain: KeychainStore

    private enum Key {
        static let session = "session"
        static let account = "account"
    }

    public init(client: SynapseClient, store: LocalStore, sync: SyncEngine, keychain: KeychainStore) {
        self.client = client
        self.store = store
        self.sync = sync
        self.keychain = keychain
    }

    public func currentAccount() async -> Account? {
        (try? keychain.value(Account.self, forKey: Key.account)) ?? nil
    }

    public func register(username: String, password: String) async throws -> Account {
        do {
            return try await authenticate(
                credentials: .password(username: username, password: password, register: true),
                username: username
            )
        } catch {
            throw ErrorMapping.mapAuth(error, registering: true)
        }
    }

    public func login(username: String, password: String) async throws -> Account {
        do {
            return try await authenticate(
                credentials: .password(username: username, password: password, register: false),
                username: username
            )
        } catch {
            throw ErrorMapping.mapAuth(error, registering: false)
        }
    }

    /// Reconnects with the stored bearer token.
    ///
    /// The stored *session* (which carries the resume token) is handed to the
    /// client rather than just the token, so a reconnect that happens soon after
    /// a drop can `RESUME` and get its missed frames replayed instead of
    /// refetching history for every open chat.
    public func restoreSession() async throws -> Account {
        guard
            let session = (try? keychain.value(SynapseClient.Session.self, forKey: Key.session)) ?? nil,
            let account = await currentAccount()
        else {
            throw AppError.unauthorized
        }
        return try await ErrorMapping.mapped {
            await client.setDeviceID(DeviceIdentity.current(keychain: keychain))
            let refreshed = try await client.connect(session: session)
            try? keychain.set(refreshed, forKey: Key.session)
            await sync.start(userID: refreshed.userID)
            try? await store.setMeta(LocalStore.MetaKey.userID, refreshed.userID)
            return account
        }
    }

    public func logout() async {
        await client.reset()
        await sync.stop()
        try? keychain.remove(key: Key.session)
        try? keychain.remove(key: Key.account)
        // The cache goes too. A messenger that shows the previous user's chats
        // after a logout has leaked them, whatever the login screen says.
        try? await store.wipe()
    }

    public func connectionStatus() -> AsyncStream<ConnectionStatus> {
        AsyncStream { continuation in
            let task = Task {
                for await status in await sync.connectionStatus() {
                    continuation.yield(status)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    public func sessionExpirations() -> AsyncStream<Void> {
        AsyncStream { continuation in
            let task = Task {
                for await _ in await sync.sessionExpirations() {
                    continuation.yield(())
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: - Private

    private func authenticate(
        credentials: SynapseClient.Credentials,
        username: String
    ) async throws -> Account {
        let deviceID = DeviceIdentity.current(keychain: keychain)
        await client.setDeviceID(deviceID)

        let session = try await client.connect(credentials: credentials)

        // `AUTH_OK` returns ids and tokens but no username — the protocol has no
        // way to ask for one later either — so the value the person typed is the
        // only name we will ever have for ourselves.
        let account = Account(
            userID: session.userID,
            deviceID: session.deviceID,
            username: username
        )
        try? keychain.set(session, forKey: Key.session)
        try? keychain.set(account, forKey: Key.account)
        try? await store.setMeta(LocalStore.MetaKey.userID, session.userID)
        await sync.start(userID: session.userID)
        return account
    }
}
