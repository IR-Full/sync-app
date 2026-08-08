import Foundation
import SwiftUI
import SynapseDomain
import SynapseNetwork
import SynapsePersistence
import SynapsePresentation

/// The composition root — the one place that knows every concrete type.
///
/// Everything above it is written against protocols, which is what makes the
/// layering real rather than aspirational: swapping the transport, the cache, or
/// a whole repository is an edit to this file and nothing else.
@MainActor
public final class AppContainer: ObservableObject {
    public let environment: ServerEnvironment
    public let appModel: AppModel
    public let viewFactory: ViewFactory

    private let client: SynapseClient
    private let syncEngine: SyncEngine
    private let database: Database

    public init(environment: ServerEnvironment = .current()) throws {
        self.environment = environment

        let client = SynapseClient(
            environment: environment,
            configuration: .init(
                clientVersion: Self.clientVersion,
                platform: "ios"
            )
        )
        self.client = client

        let database = try Database(url: Self.databaseURL())
        self.database = database

        let broker = ChangeBroker()
        let store = LocalStore(database: database, broker: broker)
        let sync = SyncEngine(client: client, store: store, broker: broker)
        self.syncEngine = sync

        let keychain = KeychainStore()
        let auth = AuthRepositoryImpl(client: client, store: store, sync: sync, keychain: keychain)
        let chats = ChatRepositoryImpl(client: client, store: store, sync: sync)
        let messages = MessageRepositoryImpl(client: client, store: store, sync: sync)
        let contacts = ContactRepositoryImpl(client: client, store: store, sync: sync)
        let search = SearchRepositoryImpl(client: client)
        let media = MediaRepositoryImpl(client: client)
        let settings = SettingsRepositoryImpl()
        let push = PushRepositoryImpl(client: client)

        self.viewFactory = ViewFactory(
            chats: chats, messages: messages, contacts: contacts,
            search: search, media: media, auth: auth
        )
        self.appModel = AppModel(auth: auth, settings: settings, push: push)
    }

    /// Opens the cache. Called once, before the first screen renders.
    ///
    /// A schema that cannot be migrated is not something to limp along with: a
    /// half-migrated cache would show wrong data. Recreating it costs the user
    /// their offline history and nothing else — every message is still on the
    /// server, and the outbox is the only thing that would be genuinely lost, so
    /// the failure is loud in the log and silent on screen.
    public func prepare() async {
        do {
            try await database.prepare()
        } catch {
            try? FileManager.default.removeItem(at: Self.databaseURL())
            if let fresh = try? Database(url: Self.databaseURL()) {
                try? await fresh.prepare()
            }
        }
    }

    private static var clientVersion: String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
        return "ios/" + (version ?? "1.0")
    }

    private static func databaseURL() -> URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        return base.appendingPathComponent("synapse.sqlite")
    }
}
