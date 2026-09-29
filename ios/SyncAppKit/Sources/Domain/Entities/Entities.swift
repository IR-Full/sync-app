import Foundation

// The domain layer. It depends on nothing — not on the wire protocol, not on
// SQLite, not on SwiftUI — so these types are free to describe what the app
// means rather than what the server happens to send.
//
// Where a field exists only because the protocol has no way to provide it, that
// is called out. There are three such places, and pretending otherwise would
// make the model lie.

/// A conversation.
public struct Chat: Identifiable, Hashable, Sendable {
    public enum Kind: String, Sendable, Codable {
        case direct, group, channel
        /// End-to-end encrypted, two parties, Premium-gated.
        ///
        /// A chat TYPE rather than a mode, which is the whole design: it is created,
        /// listed, opened and read like any other chat, and the only differences the
        /// user sees are a lock badge and the safety number. Making it a separate
        /// surface — the shape it had before — meant secret conversations had no
        /// history, no unread count and no place in the list, so nobody used them.
        case secret

        /// Exactly two participants, so the row is named after the peer rather than
        /// a title. Both `direct` and `secret` qualify, and forgetting the second is
        /// why secret chats rendered with a blank name.
        public var isTwoParty: Bool { self == .direct || self == .secret }

        /// The messages never leave the devices in plaintext.
        public var isEndToEnd: Bool { self == .secret }
    }

    public var id: String
    public var kind: Kind
    public var title: String
    /// Public handle (`t.me/<username>` style), when the chat has one.
    public var username: String?
    public var ownerID: String
    /// For a direct chat: the other participant. The gateway never sends a
    /// member list for 1:1 chats, so this is filled in from whoever we resolved
    /// the chat *from* (an `@username` target, or the sender of the first
    /// inbound message).
    public var peerUserID: String?
    public var lastMessagePreview: String
    public var lastMessageAt: Date?
    public var lastSeq: UInt64
    /// Highest `chat_seq` this user has marked read. Unread is derived from it
    /// rather than stored, so a read receipt from another device fixes the badge
    /// without a separate counter to keep in sync.
    public var lastReadSeq: UInt64

    /// When notifications resume. `nil` is "not muted".
    ///
    /// A DEADLINE, not a flag. "Mute for eight hours" is what muting almost always
    /// means, and the boolean this replaced could not express it — so the only
    /// available mute was forever, which is why a mute button was never added.
    public var mutedUntil: Date?
    /// Sorted above everything else, regardless of activity.
    public var isPinned: Bool
    /// Hidden from the main list. Not deleted, and not `hidden` — that column is the
    /// cache's own bookkeeping, while this is a per-member setting the server stores.
    public var isArchived: Bool
    /// The sort key: the last message, or the chat's creation time when there is
    /// none. Falling back to creation is what puts a brand-new empty chat at the top
    /// where the user just made it, rather than at the bottom.
    public var lastActivityAt: Date?

    /// Whether notifications are suppressed right NOW.
    ///
    /// Computed from the deadline rather than stored, so a mute expires on its own
    /// without anything having to run at the moment it does.
    public var isMuted: Bool {
        guard let mutedUntil else { return false }
        return mutedUntil > Date()
    }

    public init(
        id: String,
        kind: Kind,
        title: String,
        username: String? = nil,
        ownerID: String = "",
        peerUserID: String? = nil,
        lastMessagePreview: String = "",
        lastMessageAt: Date? = nil,
        lastSeq: UInt64 = 0,
        lastReadSeq: UInt64 = 0,
        mutedUntil: Date? = nil,
        isPinned: Bool = false,
        isArchived: Bool = false,
        lastActivityAt: Date? = nil
    ) {
        self.id = id
        self.kind = kind
        self.title = title
        self.username = username
        self.ownerID = ownerID
        self.peerUserID = peerUserID
        self.lastMessagePreview = lastMessagePreview
        self.lastMessageAt = lastMessageAt
        self.lastSeq = lastSeq
        self.lastReadSeq = lastReadSeq
        self.mutedUntil = mutedUntil
        self.isPinned = isPinned
        self.isArchived = isArchived
        // Falls back to the last message, so a caller that knows only that is not
        // forced to pass the same value twice — and a chat with neither sorts by id,
        // which is stable rather than arbitrary.
        self.lastActivityAt = lastActivityAt ?? lastMessageAt
    }
}

/// A chat with the counters the list screen needs, computed by the repository.
public struct ChatSummary: Identifiable, Hashable, Sendable {
    public var chat: Chat
    public var unreadCount: Int
    /// Someone (not us) is typing right now.
    public var typingUserIDs: [String]
    public var isPeerOnline: Bool

    public var id: String { chat.id }

    public init(chat: Chat, unreadCount: Int = 0, typingUserIDs: [String] = [], isPeerOnline: Bool = false) {
        self.chat = chat
        self.unreadCount = unreadCount
        self.typingUserIDs = typingUserIDs
        self.isPeerOnline = isPeerOnline
    }
}

/// How far along the send pipeline a message is.
///
/// `sending` is a local-only state: the message exists in the outbox and in the
/// UI but has no server id yet. It becomes `sent` when the `SEND_ACK` lands —
/// which is also when it first acquires a `chat_seq` and therefore a position in
/// the ordering.
public enum MessageState: String, Sendable, Codable {
    case sending
    case sent
    case read
    case failed
}

/// A message, local or remote.
public struct Message: Identifiable, Hashable, Sendable {
    /// The server's snowflake once acked; before that, the dedup key. Using the
    /// dedup key as the provisional id is what lets the optimistic row and the
    /// acked row be the same row rather than a duplicate to reconcile.
    public var id: String
    public var chatID: String
    public var senderID: String
    /// Per-chat ordering position. Gap-free and strictly increasing server-side;
    /// `0` while the message is still in the outbox.
    public var seq: UInt64
    public var text: String
    public var sentAt: Date
    public var state: MessageState
    public var isEdited: Bool
    public var isDeleted: Bool
    public var replyToID: String?
    public var attachment: Attachment?
    public var forwardedFrom: ForwardOrigin?
    /// Self-destruct deadline; nil = never.
    public var expiresAt: Date?
    public var reactions: [String: Int]
    /// Client idempotency key. Stable across retries — this is what makes the
    /// outbox safe to flush more than once.
    public var dedupKey: String

    public init(
        id: String,
        chatID: String,
        senderID: String,
        seq: UInt64 = 0,
        text: String = "",
        sentAt: Date = .init(),
        state: MessageState = .sent,
        isEdited: Bool = false,
        isDeleted: Bool = false,
        replyToID: String? = nil,
        attachment: Attachment? = nil,
        forwardedFrom: ForwardOrigin? = nil,
        expiresAt: Date? = nil,
        reactions: [String: Int] = [:],
        dedupKey: String = ""
    ) {
        self.id = id
        self.chatID = chatID
        self.senderID = senderID
        self.seq = seq
        self.text = text
        self.sentAt = sentAt
        self.state = state
        self.isEdited = isEdited
        self.isDeleted = isDeleted
        self.replyToID = replyToID
        self.attachment = attachment
        self.forwardedFrom = forwardedFrom
        self.expiresAt = expiresAt
        self.reactions = reactions
        self.dedupKey = dedupKey
    }
}

/// Typed media metadata. The bytes live in the media service; this describes
/// them well enough to render a placeholder before anything is downloaded.
public struct Attachment: Hashable, Sendable, Codable {
    public enum Kind: String, Sendable, Codable {
        case file, image, video, voice, videoNote = "video_note"
        case unknown
    }

    public var kind: Kind
    public var mediaRef: String
    public var filename: String
    public var mime: String
    public var size: Int64
    public var durationMs: Int64
    public var waveform: [Int32]
    public var width: Int32
    public var height: Int32
    public var thumbRef: String

    public init(
        kind: Kind, mediaRef: String, filename: String = "", mime: String = "",
        size: Int64 = 0, durationMs: Int64 = 0, waveform: [Int32] = [],
        width: Int32 = 0, height: Int32 = 0, thumbRef: String = ""
    ) {
        self.kind = kind
        self.mediaRef = mediaRef
        self.filename = filename
        self.mime = mime
        self.size = size
        self.durationMs = durationMs
        self.waveform = waveform
        self.width = width
        self.height = height
        self.thumbRef = thumbRef
    }
}

/// Provenance of a forwarded message — a snapshot that outlives the original.
public struct ForwardOrigin: Hashable, Sendable, Codable {
    public var chatID: String
    public var messageID: String
    public var senderID: String

    public init(chatID: String, messageID: String, senderID: String) {
        self.chatID = chatID
        self.messageID = messageID
        self.senderID = senderID
    }
}

/// Another person.
///
/// The gateway resolves `@username` but never *returns* a user record — there is
/// no "get user" message — so `username` is only known for users we reached via
/// an `@handle`, and `displayName` is only ever a local label from the address
/// book. Rendering falls back to the username, then to the id.
public struct User: Identifiable, Hashable, Sendable {
    public var id: String
    public var username: String?
    public var displayName: String?
    public var isOnline: Bool
    public var lastSeenAt: Date?

    public init(
        id: String, username: String? = nil, displayName: String? = nil,
        isOnline: Bool = false, lastSeenAt: Date? = nil
    ) {
        self.id = id
        self.username = username
        self.displayName = displayName
        self.isOnline = isOnline
        self.lastSeenAt = lastSeenAt
    }

    /// What to show. Never the raw id if we have anything better.
    public var bestName: String {
        if let displayName, !displayName.isEmpty { return displayName }
        if let username, !username.isEmpty { return "@" + username }
        return id
    }
}

/// An address-book entry.
public struct Contact: Identifiable, Hashable, Sendable {
    public var userID: String
    public var name: String
    public var isBlocked: Bool
    public var updatedAt: Date

    public var id: String { userID }

    public init(userID: String, name: String = "", isBlocked: Bool = false, updatedAt: Date = .init()) {
        self.userID = userID
        self.name = name
        self.isBlocked = isBlocked
        self.updatedAt = updatedAt
    }
}

/// The signed-in identity, as the SERVER reports it.
///
/// `AUTH_OK` carries the profile on every connect, so none of this is guessed
/// from what the user typed — which matters on a token login, where nothing was
/// typed at all. `PROFILE_SET` is the only writer of the name and avatar, and a
/// change made on another device arrives as an unsolicited `PROFILE`.
public struct Account: Hashable, Sendable, Codable {
    public var userID: String
    public var deviceID: String
    public var username: String
    public var displayName: String
    /// A `media_ref` into the media service; empty when the user has no picture.
    public var avatarRef: String

    public init(
        userID: String,
        deviceID: String,
        username: String,
        displayName: String = "",
        avatarRef: String = ""
    ) {
        self.userID = userID
        self.deviceID = deviceID
        self.username = username
        self.displayName = displayName
        self.avatarRef = avatarRef
    }

    /// What to show for ourselves. Never the raw id if there is anything better.
    public var bestName: String {
        if !displayName.isEmpty { return displayName }
        if !username.isEmpty { return "@" + username }
        return userID
    }

    private enum CodingKeys: String, CodingKey {
        case userID, deviceID, username, displayName, avatarRef
    }

    /// Hand-written for the same reason as `SyncAppClient.Session`: a synthesized
    /// `Decodable` fails on a missing key even when the property has a default,
    /// so an account persisted before the profile existed would not load.
    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        userID = try c.decode(String.self, forKey: .userID)
        deviceID = try c.decode(String.self, forKey: .deviceID)
        username = try c.decode(String.self, forKey: .username)
        displayName = try c.decodeIfPresent(String.self, forKey: .displayName) ?? ""
        avatarRef = try c.decodeIfPresent(String.self, forKey: .avatarRef) ?? ""
    }
}

/// Someone's public profile, as `PROFILE_GET` returns it.
public struct Profile: Hashable, Sendable {
    public var userID: String
    public var username: String
    public var displayName: String
    public var avatarRef: String
    /// A paying account, for the badge beside the name. Decided by the server,
    /// which reports false where the deployment sells nothing: there every account
    /// is entitled to everything, and a badge everybody has is not a badge.
    public var premium: Bool

    public init(
        userID: String,
        username: String,
        displayName: String = "",
        avatarRef: String = "",
        premium: Bool = false
    ) {
        self.userID = userID
        self.username = username
        self.displayName = displayName
        self.avatarRef = avatarRef
        self.premium = premium
    }

    public var bestName: String {
        if !displayName.isEmpty { return displayName }
        if !username.isEmpty { return "@" + username }
        return userID
    }
}

/// Connection state as the UI cares about it.
public enum ConnectionStatus: String, Sendable, Equatable {
    case offline
    case connecting
    case online
}
