import Foundation

// Envelope bodies for the chat list and for profiles. Field numbers come from
// server/proto/syncapp/v1/body.proto and must never be renumbered independently
// of it.

/// `CHAT_LIST` — ask for the chats this account belongs to.
///
/// Paged by keyset over the chat id (`after` = the last id of the previous page,
/// empty = from the start) rather than by offset: a page has to stay stable
/// while the list changes underneath it, and the id is the only cursor that does.
public struct ChatListBody: ProtoMessage, Sendable, Equatable {
    public var after = ""
    public var limit: Int32 = 0

    public init(after: String = "", limit: Int32 = 0) {
        self.after = after
        self.limit = limit
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, after)
        w.int32(2, limit)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: after = try r.string()
            case 2: limit = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// One row of the chat list: enough to render an entry without a follow-up
/// request per chat.
public struct ChatSummaryBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var type = ""
    public var title = ""
    public var ownerID = ""
    public var username = ""
    /// The chat's newest position, so a client knows what it still has to backfill.
    public var lastSeq: UInt64 = 0
    /// This account's role here: member | admin | owner.
    public var myRole = ""
    /// Filled for DIRECT chats only. A 1:1 chat has no title, so without the
    /// peer there is nothing to name the row after.
    public var peerID = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, type)
        w.string(3, title)
        w.string(4, ownerID)
        w.string(5, username)
        w.uint64(6, lastSeq)
        w.string(7, myRole)
        w.string(8, peerID)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: type = try r.string()
            case 3: title = try r.string()
            case 4: ownerID = try r.string()
            case 5: username = try r.string()
            case 6: lastSeq = try r.uint64()
            case 7: myRole = try r.string()
            case 8: peerID = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `CHATS` — one page of the caller's chats plus the cursor to resume from.
public struct ChatsBody: ProtoMessage, Sendable, Equatable {
    public var chats: [ChatSummaryBody] = []
    public var nextAfter = ""
    public var done = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.repeatedMessage(1, chats)
        w.string(2, nextAfter)
        w.bool(3, done)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chats.append(try r.message(ChatSummaryBody.self))
            case 2: nextAfter = try r.string()
            case 3: done = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `PROFILE_GET` — read a user's public profile.
///
/// `target` is a user id or `"@username"`, which makes this the user lookup as
/// well: there is no directory and no prefix search, so an exact handle is all a
/// client can be expected to know about a stranger. An empty target means "me".
public struct ProfileGetBody: ProtoMessage, Sendable, Equatable {
    public var target = ""

    public init(target: String = "") {
        self.target = target
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, target)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: target = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `PROFILE_SET` — change our OWN profile; the protocol cannot address anyone
/// else's.
///
/// An empty field means "leave as is", because proto3 cannot tell an absent
/// string from an empty one. Removing the avatar is therefore an explicit flag
/// rather than an empty ref.
public struct ProfileSetBody: ProtoMessage, Sendable, Equatable {
    public var displayName = ""
    public var avatarRef = ""
    public var clearAvatar = false

    public init(displayName: String = "", avatarRef: String = "", clearAvatar: Bool = false) {
        self.displayName = displayName
        self.avatarRef = avatarRef
        self.clearAvatar = clearAvatar
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, displayName)
        w.string(2, avatarRef)
        w.bool(3, clearAvatar)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: displayName = try r.string()
            case 2: avatarRef = try r.string()
            case 3: clearAvatar = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `PROFILE` — a user's public profile.
///
/// Arrives as the reply to `PROFILE_GET`/`PROFILE_SET`, and unsolicited when
/// another device of this account changed it. It carries nothing private: the
/// same body goes to the owner and to a stranger who resolved their handle.
public struct ProfileBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var username = ""
    public var displayName = ""
    public var avatarRef = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.string(2, username)
        w.string(3, displayName)
        w.string(4, avatarRef)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: username = try r.string()
            case 3: displayName = try r.string()
            case 4: avatarRef = try r.string()
            default: try r.skip(f)
            }
        }
    }
}
