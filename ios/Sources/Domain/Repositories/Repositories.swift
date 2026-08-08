import Foundation

// Repository contracts. The layer above (view models, use cases) sees only
// these; whether an answer came from SQLite, from the gateway, or from both is
// the repository's business.
//
// Reads are `AsyncStream`s of the *whole* current value rather than deltas. For
// a chat list and a message page that is the right trade: the cache is the
// single source of truth, so a stream of snapshots cannot drift out of sync with
// it the way an event log can, and SwiftUI diffs the snapshot anyway.

/// Authentication and session lifetime.
public protocol AuthRepository: Sendable {
    /// The stored account, if a previous session can be restored.
    func currentAccount() async -> Account?

    /// Registers a new account and connects.
    func register(username: String, password: String) async throws -> Account

    /// Logs in and connects.
    func login(username: String, password: String) async throws -> Account

    /// Reconnects using the stored token. Throws if there is nothing to restore
    /// or the credential is dead.
    func restoreSession() async throws -> Account

    /// Disconnects and wipes every local trace of the user: tokens, cache,
    /// outbox. Anything less would leave the next person to open the app looking
    /// at someone else's messages.
    func logout() async

    /// Live connection state for the UI's status banner.
    func connectionStatus() -> AsyncStream<ConnectionStatus>

    /// Fires when the server invalidated the session underneath us (revoked from
    /// another device, expired). The app must return to the login screen.
    func sessionExpirations() -> AsyncStream<Void>
}

/// The chat list.
public protocol ChatRepository: Sendable {
    /// Cache-backed, updated as messages and receipts arrive.
    func observeChats() -> AsyncStream<[ChatSummary]>

    func chat(id: String) async -> Chat?

    /// Opens (creating if needed) the 1:1 chat with `@username`.
    func openDirectChat(username: String) async throws -> Chat

    func createGroup(title: String, memberHandles: [String], isChannel: Bool) async throws -> Chat

    /// Joins by invite code or `@handle`; returns the chat id.
    func join(code: String?, handle: String?) async throws -> String

    func setMuted(chatID: String, muted: Bool) async

    /// Deletes the local cache of a chat. There is no server-side "leave chat"
    /// in this protocol, so this is explicitly a local hide, not a leave.
    func hideLocally(chatID: String) async
}

/// Messages within one chat.
public protocol MessageRepository: Sendable {
    /// The cached page, oldest → newest.
    func observeMessages(chatID: String) -> AsyncStream<[Message]>

    /// Pulls the newest page, regardless of what is cached.
    ///
    /// Needed on every chat open because paging alone cannot fill a *forward*
    /// gap: if the app was offline long enough for the session's replay buffer
    /// to expire, the messages sent in that window are newer than everything we
    /// hold, and `loadOlder` walks the wrong way.
    func refreshLatest(chatID: String) async throws

    /// Pulls one page older than what is cached. Returns false when the server
    /// says there is no more history.
    @discardableResult
    func loadOlder(chatID: String) async throws -> Bool

    /// Queues a message: it is written to the cache and the outbox first and
    /// sent second, so composing works with no connection at all.
    func send(chatID: String, text: String, replyTo: String?, attachment: Attachment?) async throws

    /// Retries one failed outbox entry.
    func retry(messageID: String) async throws

    func edit(chatID: String, messageID: String, text: String) async throws
    func delete(chatID: String, messageID: String, forEveryone: Bool) async throws
    func toggleReaction(chatID: String, messageID: String, emoji: String) async throws

    /// Marks everything up to `seq` read, locally and on the server.
    func markRead(chatID: String, upToSeq: UInt64) async

    func setTyping(chatID: String, active: Bool) async

    /// Who is currently typing in this chat.
    func observeTyping(chatID: String) -> AsyncStream<Set<String>>

    /// Saves what the user is composing.
    ///
    /// A draft is private to the user and mirrored by the gateway to their
    /// *other* devices — it is routed per-user, never to the chat — so this is
    /// cross-device continuation, not a shared scratchpad.
    func saveDraft(chatID: String, text: String, replyTo: String?) async

    /// The draft for this chat, updated when another device changes it.
    func observeDraft(chatID: String) -> AsyncStream<String>
}

/// Media. The bytes never travel as protocol frames: the gateway mints an
/// HMAC-signed, expiring URL and the blob goes over HTTP, while the message
/// carries only a short `media_ref` plus the metadata needed to render a
/// placeholder before anything is downloaded.
public protocol MediaRepository: Sendable {
    /// Uploads bytes and returns the attachment to hang on a message.
    ///
    /// Requires a connection: the upload ticket is minted over the protocol and
    /// the signed size is binding, so there is nothing sensible to queue offline.
    func upload(
        data: Data, filename: String, mime: String, kind: Attachment.Kind, extra: MediaMetadata
    ) async throws -> Attachment

    /// A local file URL for an attachment, downloading it if needed.
    func fileURL(for attachment: Attachment) async throws -> URL

    /// A cached file URL, or nil if it has not been downloaded yet. Cheap and
    /// synchronous-ish, so a list row can ask without starting a transfer.
    func cachedURL(for attachment: Attachment) async -> URL?
}

/// Kind-specific extras a caller may know at upload time (image dimensions, a
/// voice note's duration). Everything is optional because the sender is not
/// always in a position to measure it.
public struct MediaMetadata: Sendable, Equatable {
    public var width: Int32
    public var height: Int32
    public var durationMs: Int64
    public var waveform: [Int32]

    public init(width: Int32 = 0, height: Int32 = 0, durationMs: Int64 = 0, waveform: [Int32] = []) {
        self.width = width
        self.height = height
        self.durationMs = durationMs
        self.waveform = waveform
    }
}

/// The address book and user lookup.
public protocol ContactRepository: Sendable {
    func observeContacts() -> AsyncStream<[Contact]>
    func sync() async throws
    func add(handle: String, name: String) async throws
    func remove(userID: String) async throws
    func setBlocked(userID: String, blocked: Bool) async throws
    /// Best-effort local lookup used for rendering names next to a user id.
    func user(id: String) async -> User?
}

/// Search across the user's own chats (server-side, permission-filtered).
public protocol SearchRepository: Sendable {
    func search(query: String) async throws -> [SearchResult]
}

public struct SearchResult: Identifiable, Hashable, Sendable {
    public var messageID: String
    public var chatID: String
    public var senderID: String
    public var seq: UInt64
    public var text: String

    public var id: String { messageID }

    public init(messageID: String, chatID: String, senderID: String, seq: UInt64, text: String) {
        self.messageID = messageID
        self.chatID = chatID
        self.senderID = senderID
        self.seq = seq
        self.text = text
    }
}

/// User-visible settings that live only on this device.
public protocol SettingsRepository: Sendable {
    func settings() -> AsyncStream<AppSettings>
    func current() async -> AppSettings
    func update(_ transform: @Sendable (inout AppSettings) -> Void) async
}

/// Device-local preferences and profile.
///
/// `displayName` and `avatarSymbol` are here — and not on `Account` — because
/// the gateway has no profile API at all: `display_name` can only be set at
/// registration (and the gateway passes an empty one), and no message type
/// updates it afterwards. Storing them locally is honest about that; the
/// settings screen says so too.
public struct AppSettings: Equatable, Sendable, Codable {
    public enum Theme: String, Sendable, Codable, CaseIterable {
        case system, light, dark
    }

    public enum Language: String, Sendable, Codable, CaseIterable {
        case system, ru, en
    }

    public var theme: Theme
    public var language: Language
    public var pushEnabled: Bool
    public var showTypingIndicators: Bool
    public var localDisplayName: String
    public var avatarSymbol: String

    public init(
        theme: Theme = .system,
        language: Language = .system,
        pushEnabled: Bool = true,
        showTypingIndicators: Bool = true,
        localDisplayName: String = "",
        avatarSymbol: String = ""
    ) {
        self.theme = theme
        self.language = language
        self.pushEnabled = pushEnabled
        self.showTypingIndicators = showTypingIndicators
        self.localDisplayName = localDisplayName
        self.avatarSymbol = avatarSymbol
    }
}

/// APNs registration.
public protocol PushRepository: Sendable {
    /// Hands the APNs device token to the gateway. An empty token clears it,
    /// which is what "notifications off" should do — stop them at the source.
    func register(deviceToken: Data) async
    func unregister() async
}
