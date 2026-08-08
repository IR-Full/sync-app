import Foundation
import SynapseDomain
import SynapseNetwork

/// The address book, plus the little the app can know about other users.
public final class ContactRepositoryImpl: ContactRepository, @unchecked Sendable {
    private let client: SynapseClient
    private let store: LocalStore
    // Named `syncEngine`, not `sync`, so it does not collide with the protocol's
    // own `sync()` method.
    private let syncEngine: SyncEngine

    public init(client: SynapseClient, store: LocalStore, sync: SyncEngine) {
        self.client = client
        self.store = store
        self.syncEngine = sync
    }

    public func observeContacts() -> AsyncStream<[Contact]> {
        AsyncStream { continuation in
            let task = Task {
                for await _ in await store.changes(.contacts) {
                    continuation.yield((try? await store.contacts()) ?? [])
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    public func sync() async throws {
        await syncEngine.syncContacts()
    }

    /// `target` may be a user id or `@username`; the gateway resolves it, which
    /// is the only user lookup the protocol offers.
    public func add(handle: String, name: String) async throws {
        let normalized = OpenDirectChatUseCase.normalize(handle)
        let page = try await ErrorMapping.mapped {
            try await client.addContact(target: "@" + normalized, name: name)
        }
        let contacts = page.contacts.map(WireMapping.contact(from:))
        try await store.upsertContacts(contacts)
        // Now that we know id ↔ handle, remember it: nothing else in the
        // protocol will ever tell us this user's name again.
        for contact in contacts {
            try? await store.upsertUser(User(
                id: contact.userID,
                username: normalized,
                displayName: contact.name.nilIfEmpty
            ))
        }
    }

    public func remove(userID: String) async throws {
        try await ErrorMapping.mapped { try await client.removeContact(target: userID) }
        try await store.removeContact(userID: userID)
    }

    public func setBlocked(userID: String, blocked: Bool) async throws {
        try await ErrorMapping.mapped { try await client.setBlocked(target: userID, blocked: blocked) }
        if var contact = try await store.contacts().first(where: { $0.userID == userID }) {
            contact.isBlocked = blocked
            contact.updatedAt = Date()
            try await store.upsertContacts([contact])
        } else {
            // Blocking works on strangers too, so there may be no contact row to
            // update — create one carrying just the block.
            try await store.upsertContacts([
                Contact(userID: userID, isBlocked: blocked, updatedAt: Date())
            ])
        }
    }

    public func user(id: String) async -> User? {
        (try? await store.user(id: id)) ?? nil
    }
}

/// Full-text search. Server-side, ranked, and filtered to chats the user is
/// actually in — so there is nothing to filter again here.
public final class SearchRepositoryImpl: SearchRepository, @unchecked Sendable {
    private let client: SynapseClient

    public init(client: SynapseClient) {
        self.client = client
    }

    public func search(query: String) async throws -> [SearchResult] {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return [] }
        return try await ErrorMapping.mapped {
            try await client.search(query: trimmed).map {
                SearchResult(
                    messageID: $0.messageID, chatID: $0.chatID, senderID: $0.senderID,
                    seq: $0.seq, text: $0.text
                )
            }
        }
    }
}

/// Device-local preferences.
///
/// `UserDefaults`, deliberately: none of this is a credential, and it needs to
/// be readable synchronously at launch so the first frame renders in the right
/// theme instead of flashing the wrong one.
public final class SettingsRepositoryImpl: SettingsRepository, @unchecked Sendable {
    private let defaults: UserDefaults
    private let key = "chat.synapse.settings"
    private let lock = NSLock()
    private var subscribers: [UUID: AsyncStream<AppSettings>.Continuation] = [:]

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    public func settings() -> AsyncStream<AppSettings> {
        AsyncStream { continuation in
            let id = UUID()
            lock.lock()
            subscribers[id] = continuation
            lock.unlock()
            continuation.yield(read())
            continuation.onTermination = { [weak self] _ in
                guard let self else { return }
                self.lock.lock()
                self.subscribers[id] = nil
                self.lock.unlock()
            }
        }
    }

    public func current() async -> AppSettings { read() }

    public func update(_ transform: @Sendable (inout AppSettings) -> Void) async {
        var settings = read()
        transform(&settings)
        if let data = try? JSONEncoder().encode(settings) {
            defaults.set(data, forKey: key)
        }
        lock.lock()
        let listeners = Array(subscribers.values)
        lock.unlock()
        for continuation in listeners { continuation.yield(settings) }
    }

    private func read() -> AppSettings {
        guard
            let data = defaults.data(forKey: key),
            let settings = try? JSONDecoder().decode(AppSettings.self, from: data)
        else {
            return AppSettings()
        }
        return settings
    }
}

/// APNs registration.
///
/// The gateway stores one token per *device row*, and fans a push out to every
/// device of the recipient — so registering the wrong (or a stale) token means
/// silence, not a duplicate. Clearing it on "notifications off" is what stops
/// the push at the source instead of at the device.
public final class PushRepositoryImpl: PushRepository, @unchecked Sendable {
    private let client: SynapseClient

    public init(client: SynapseClient) {
        self.client = client
    }

    public func register(deviceToken: Data) async {
        // APNs hands over raw bytes; the provider API wants them hex-encoded.
        let hex = deviceToken.map { String(format: "%02x", $0) }.joined()
        try? await client.registerPushToken(hex)
    }

    public func unregister() async {
        try? await client.registerPushToken("")
    }
}
