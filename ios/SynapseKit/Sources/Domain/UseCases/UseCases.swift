import Foundation

/// Server-side limits, mirrored so the UI can refuse input before a round trip
/// instead of after one. Kept together and named after their source, because a
/// constant copied from another codebase is only safe if you can find it again.
public enum ServerLimits {
    /// `message.MaxTextLen` — bytes, not characters. UTF-8 is what travels.
    public static let maxMessageBytes = 8192
    /// `gateway.maxChatTitle`.
    public static let maxChatTitleLength = 128
    /// `gateway.maxCreateMembers`.
    public static let maxInitialMembers = 200
    /// `gateway.DefaultConfig().SendRate` — per-connection sends per second.
    public static let sendsPerSecond = 20
    /// `media.New`'s `maxSize`. Checked before asking for a ticket: the size is
    /// part of what the ticket signs, and the upload handler holds the body to
    /// it exactly, so an oversized file fails at `MEDIA_INIT` either way.
    public static let maxMediaBytes: Int64 = 100 << 20
}

public enum ValidationError: Error, Equatable, Sendable {
    case emptyMessage
    case messageTooLong(bytes: Int, limit: Int)
    case emptyTitle
    case titleTooLong(limit: Int)
    case tooManyMembers(limit: Int)
    case emptyCredential
    case usernameInvalid
}

/// Sends a message after the checks the server would otherwise make for us.
///
/// This exists as a use case rather than a repository method because "may this
/// text be sent" is a rule, and rules that live in a view model get re-typed
/// (differently) in the next view model.
public struct SendMessageUseCase: Sendable {
    private let messages: any MessageRepository

    public init(messages: any MessageRepository) {
        self.messages = messages
    }

    public func callAsFunction(
        chatID: String,
        text: String,
        replyTo: String? = nil,
        attachment: Attachment? = nil
    ) async throws {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        // A message with an attachment may have no text at all — a photo on its
        // own is a message. Only a message with neither is empty.
        if attachment == nil {
            guard !trimmed.isEmpty else { throw ValidationError.emptyMessage }
        }
        let byteCount = trimmed.utf8.count
        guard byteCount <= ServerLimits.maxMessageBytes else {
            throw ValidationError.messageTooLong(bytes: byteCount, limit: ServerLimits.maxMessageBytes)
        }
        try await messages.send(chatID: chatID, text: trimmed, replyTo: replyTo, attachment: attachment)
    }
}

/// Starts a 1:1 chat from a typed handle.
///
/// The protocol has no user directory, so "find someone" is really "normalise
/// what was typed and try to resolve it". Normalising here means the three
/// spellings a person actually types — `bob`, `@bob`, `@Bob` — all reach the
/// same chat, since the gateway lowercases the handle on its side.
public struct OpenDirectChatUseCase: Sendable {
    private let chats: any ChatRepository

    public init(chats: any ChatRepository) {
        self.chats = chats
    }

    public func callAsFunction(handle: String) async throws -> Chat {
        let normalized = Self.normalize(handle)
        guard !normalized.isEmpty else { throw ValidationError.usernameInvalid }
        return try await chats.openDirectChat(username: normalized)
    }

    public static func normalize(_ handle: String) -> String {
        handle
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .drop(while: { $0 == "@" })
            .lowercased()
    }
}

/// Creates a group or channel.
public struct CreateGroupUseCase: Sendable {
    private let chats: any ChatRepository

    public init(chats: any ChatRepository) {
        self.chats = chats
    }

    public func callAsFunction(title: String, memberHandles: [String], isChannel: Bool) async throws -> Chat {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw ValidationError.emptyTitle }
        guard trimmed.count <= ServerLimits.maxChatTitleLength else {
            throw ValidationError.titleTooLong(limit: ServerLimits.maxChatTitleLength)
        }
        let members = memberHandles
            .map { OpenDirectChatUseCase.normalize($0) }
            .filter { !$0.isEmpty }
        guard members.count <= ServerLimits.maxInitialMembers else {
            throw ValidationError.tooManyMembers(limit: ServerLimits.maxInitialMembers)
        }
        // The gateway resolves `@name` members itself, so we hand it handles
        // rather than pre-resolving each one into an id (which would be N extra
        // round trips and N accidental 1:1 chats).
        return try await chats.createGroup(
            title: trimmed,
            memberHandles: members.map { "@" + $0 },
            isChannel: isChannel
        )
    }
}

/// Signs in or registers, keeping the "which intent was this" decision explicit.
///
/// The gateway refuses to create an account on a failed login, deliberately —
/// that is what stops account-existence probing and typo'd-username takeover —
/// so the client must know which of the two it is asking for. There is no
/// "log in, or register if new" mode to offer.
public struct AuthenticateUseCase: Sendable {
    /// `Hashable`, not merely `Equatable`: this is what a `Picker` selection is
    /// tagged with, and `View.tag` requires it.
    public enum Intent: Sendable, Hashable {
        case login, register
    }

    private let auth: any AuthRepository

    public init(auth: any AuthRepository) {
        self.auth = auth
    }

    public func callAsFunction(intent: Intent, username: String, password: String) async throws -> Account {
        let user = username.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !user.isEmpty, !password.isEmpty else { throw ValidationError.emptyCredential }
        switch intent {
        case .login:
            return try await auth.login(username: user, password: password)
        case .register:
            return try await auth.register(username: user, password: password)
        }
    }
}

/// Marks a chat read, but only forwards — a receipt is a high-water mark, and
/// sending a lower one would move it backwards for every device on the account.
public struct MarkChatReadUseCase: Sendable {
    private let messages: any MessageRepository

    public init(messages: any MessageRepository) {
        self.messages = messages
    }

    public func callAsFunction(chat: Chat, upToSeq: UInt64) async {
        guard upToSeq > chat.lastReadSeq else { return }
        await messages.markRead(chatID: chat.id, upToSeq: upToSeq)
    }
}
