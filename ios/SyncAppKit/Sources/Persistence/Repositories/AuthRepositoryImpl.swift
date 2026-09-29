import Foundation
import SyncAppDomain
import SyncAppNetwork

/// Sessions, and the two credentials worth persisting.
///
/// The bearer token and the resume token go to the keychain, never to
/// `UserDefaults`: they authenticate as the user, and a plist in the app
/// container is not a place to keep that. The account record travels with them —
/// it is not a credential, but keeping identity and tokens in one store means a
/// logout cannot clear one and leave the other behind.
public final class AuthRepositoryImpl: AuthRepository, @unchecked Sendable {
    private let client: SyncAppClient
    private let store: LocalStore
    private let sync: SyncEngine
    private let keychain: KeychainStore
    /// Present only in builds with the E2E module. Logout has to reach it, because the
    /// secret-chat identity lives outside the cache that `wipe()` clears.
    private let secret: SecretChatService?

    private enum Key {
        static let session = "session"
        static let account = "account"
    }

    public init(
        client: SyncAppClient,
        store: LocalStore,
        sync: SyncEngine,
        keychain: KeychainStore,
        secret: SecretChatService? = nil
    ) {
        self.client = client
        self.store = store
        self.sync = sync
        self.keychain = keychain
        self.secret = secret
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
            let session = (try? keychain.value(SyncAppClient.Session.self, forKey: Key.session)) ?? nil,
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

            // AUTH_OK carries the profile, so a name or avatar changed while the
            // app was closed is picked up on this reconnect. Without it the
            // stored copy would only ever be as new as the last password login.
            guard !refreshed.username.isEmpty else { return account }
            var updated = account
            updated.username = refreshed.username
            updated.displayName = refreshed.displayName
            updated.avatarRef = refreshed.avatarRef
            try? keychain.set(updated, forKey: Key.account)
            return updated
        }
    }

    public func logout() async {
        await client.reset()
        await sync.stop()
        try? keychain.remove(key: Key.session)
        try? keychain.remove(key: Key.account)
        // The secret-chat identity is per ACCOUNT, so it goes as well. Leaving it would
        // have the next account publish this one's identity key — and the old peers
        // would see their pin still matching, which is the one thing a pin must not do.
        await secret?.forgetIdentity()
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

    // MARK: - Profile

    /// The account as it stands, plus every later change.
    ///
    /// Yields the stored copy first so a screen draws immediately — it came from
    /// `AUTH_OK`, so it is server state, not a guess — and then follows the
    /// gateway's mirror of changes made on this account's other devices.
    public func accountUpdates() -> AsyncStream<Account> {
        AsyncStream { continuation in
            let task = Task { [weak self] in
                guard let self else {
                    continuation.finish()
                    return
                }
                if let account = await self.currentAccount() { continuation.yield(account) }
                for await body in await self.sync.profileUpdates() {
                    guard let updated = await self.applyProfile(body) else { continue }
                    continuation.yield(updated)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    @discardableResult
    public func updateProfile(
        displayName: String?,
        avatarRef: String?,
        clearAvatar: Bool
    ) async throws -> Account {
        try await ErrorMapping.mapped {
            // Empty means "leave as is" on the wire, so nil and "" travel the
            // same way; removing the picture is the explicit flag.
            let body = try await client.setProfile(
                displayName: displayName ?? "",
                avatarRef: avatarRef ?? "",
                clearAvatar: clearAvatar
            )
            guard let account = await applyProfile(body) else { throw AppError.unauthorized }
            return account
        }
    }

    public func profile(of target: String) async throws -> Profile {
        try await ErrorMapping.mapped {
            let body = try await client.profile(target: target)
            return Profile(
                userID: body.userID,
                username: body.username,
                displayName: body.displayName,
                avatarRef: body.avatarRef,
                premium: body.premium
            )
        }
    }

    // MARK: - Private

    /// Folds a `PROFILE` body into the stored account and returns the result.
    /// Returns nil when there is no account to fold it into — a profile frame
    /// arriving after logout is not an error, just late.
    private func applyProfile(_ body: ProfileBody) async -> Account? {
        guard var account = await currentAccount() else { return nil }
        account.username = body.username.isEmpty ? account.username : body.username
        account.displayName = body.displayName
        account.avatarRef = body.avatarRef
        try? keychain.set(account, forKey: Key.account)
        return account
    }

    private func authenticate(
        credentials: SyncAppClient.Credentials,
        username: String
    ) async throws -> Account {
        let deviceID = DeviceIdentity.current(keychain: keychain)
        await client.setDeviceID(deviceID)

        let session = try await client.connect(credentials: credentials)

        // `AUTH_OK` names the account, so the typed string is only a fallback
        // for a gateway too old to send one.
        let account = Account(
            userID: session.userID,
            deviceID: session.deviceID,
            username: session.username.isEmpty ? username : session.username,
            displayName: session.displayName,
            avatarRef: session.avatarRef
        )
        try? keychain.set(session, forKey: Key.session)
        try? keychain.set(account, forKey: Key.account)
        try? await store.setMeta(LocalStore.MetaKey.userID, session.userID)
        await sync.start(userID: session.userID)
        return account
    }
}
