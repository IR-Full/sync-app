import Foundation

// Chat creation, membership, invite links, contacts, drafts and pins.

/// `CHAT_CREATE` — `type` is `group` or `channel`. Members may be user ids or
/// `"@username"`; the gateway resolves them, so a client never has to look a
/// stranger up before inviting them.
public struct ChatCreateBody: ProtoMessage, Sendable, Equatable {
    public var type = ""
    public var title = ""
    public var members: [String] = []

    public init(type: String = "group", title: String = "", members: [String] = []) {
        self.type = type
        self.title = title
        self.members = members
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, type)
        w.string(2, title)
        w.repeatedString(3, members)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: type = try r.string()
            case 2: title = try r.string()
            case 3: members.append(try r.string())
            default: try r.skip(f)
            }
        }
    }
}

/// `CHAT_INFO` — currently only the reply to a create.
public struct ChatInfoBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var type = ""
    public var title = ""
    public var ownerID = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, type)
        w.string(3, title)
        w.string(4, ownerID)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: type = try r.string()
            case 3: title = try r.string()
            case 4: ownerID = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `JOIN` — by invite code, or by `@handle` for a public chat.
public struct JoinBody: ProtoMessage, Sendable, Equatable {
    public var code = ""
    public var handle = ""

    public init(code: String = "", handle: String = "") {
        self.code = code
        self.handle = handle
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, code)
        w.string(2, handle)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: code = try r.string()
            case 2: handle = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// One live invite link. Zero `expiresAt`/`maxUses` mean unlimited.
public struct InviteLinkBody: ProtoMessage, Sendable, Equatable {
    public var code = ""
    public var chatID = ""
    public var expiresAt: Int64 = 0
    public var maxUses: Int32 = 0
    public var uses: Int32 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, code)
        w.string(2, chatID)
        w.int64(3, expiresAt)
        w.int32(4, maxUses)
        w.int32(5, uses)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: code = try r.string()
            case 2: chatID = try r.string()
            case 3: expiresAt = try r.int64()
            case 4: maxUses = try r.int32()
            case 5: uses = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `INVITES` — the catch-all reply for every membership command: a link list, a
/// join result, or an empty acknowledgement.
public struct InvitesBody: ProtoMessage, Sendable, Equatable {
    public var links: [InviteLinkBody] = []
    public var joinedChat = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.repeatedMessage(1, links)
        w.string(2, joinedChat)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: links.append(try r.message(InviteLinkBody.self))
            case 2: joinedChat = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `INVITE_CREATE`.
public struct InviteCreateBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var expiresAt: Int64 = 0
    public var maxUses: Int32 = 0

    public init(chatID: String = "", expiresAt: Int64 = 0, maxUses: Int32 = 0) {
        self.chatID = chatID
        self.expiresAt = expiresAt
        self.maxUses = maxUses
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.int64(2, expiresAt)
        w.int32(3, maxUses)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: expiresAt = try r.int64()
            case 3: maxUses = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `INVITE_LIST`.
public struct InviteListBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public init(chatID: String = "") { self.chatID = chatID }
    public func encode(to w: inout ProtoWriter) { w.string(1, chatID) }
    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `SET_ROLE` — `member` | `admin` | `owner`.
public struct SetRoleBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var userID = ""
    public var role = ""

    public init(chatID: String = "", userID: String = "", role: String = "") {
        self.chatID = chatID
        self.userID = userID
        self.role = role
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, userID)
        w.string(3, role)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: userID = try r.string()
            case 3: role = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

// MARK: - Contacts

/// `CONTACT_ADD` — `target` is a user id or `"@username"`.
public struct ContactAddBody: ProtoMessage, Sendable, Equatable {
    public var target = ""
    public var name = ""

    public init(target: String = "", name: String = "") {
        self.target = target
        self.name = name
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, target)
        w.string(2, name)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: target = try r.string()
            case 2: name = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `CONTACT_REMOVE`.
public struct ContactRemoveBody: ProtoMessage, Sendable, Equatable {
    public var target = ""
    public init(target: String = "") { self.target = target }
    public func encode(to w: inout ProtoWriter) { w.string(1, target) }
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

/// `CONTACT_SYNC` — everything changed after `since` (0 = full sync).
public struct ContactSyncBody: ProtoMessage, Sendable, Equatable {
    public var since: Int64 = 0
    public init(since: Int64 = 0) { self.since = since }
    public func encode(to w: inout ProtoWriter) { w.int64(1, since) }
    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: since = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// One address-book entry.
public struct ContactBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var name = ""
    public var blocked = false
    public var updatedAt: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.string(2, name)
        w.bool(3, blocked)
        w.int64(4, updatedAt)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: name = try r.string()
            case 3: blocked = try r.bool()
            case 4: updatedAt = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `CONTACT_LIST` — a sync page plus the cursor to resume from.
public struct ContactListBody: ProtoMessage, Sendable, Equatable {
    public var contacts: [ContactBody] = []
    public var cursor: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.repeatedMessage(1, contacts)
        w.int64(2, cursor)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: contacts.append(try r.message(ContactBody.self))
            case 2: cursor = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `BLOCK` — works on strangers, not just contacts, and cuts traffic in both
/// directions (a blocked chat stops resolving for either side).
public struct BlockBody: ProtoMessage, Sendable, Equatable {
    public var target = ""
    public var blocked = false

    public init(target: String = "", blocked: Bool = false) {
        self.target = target
        self.blocked = blocked
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, target)
        w.bool(2, blocked)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: target = try r.string()
            case 2: blocked = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

// MARK: - Drafts

/// `DRAFT_SET` — empty text clears it. Private to the user, synced across their
/// own devices only.
public struct DraftBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var text = ""
    public var replyTo = ""

    public init(chatID: String = "", text: String = "", replyTo: String = "") {
        self.chatID = chatID
        self.text = text
        self.replyTo = replyTo
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, text)
        w.string(3, replyTo)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: text = try r.string()
            case 3: replyTo = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `DRAFT_SYNC`.
public struct DraftSyncBody: ProtoMessage, Sendable, Equatable {
    public var since: Int64 = 0
    public init(since: Int64 = 0) { self.since = since }
    public func encode(to w: inout ProtoWriter) { w.int64(1, since) }
    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: since = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// One synced draft.
public struct DraftItemBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var text = ""
    public var replyTo = ""
    public var updatedAt: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, text)
        w.string(3, replyTo)
        w.int64(4, updatedAt)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: text = try r.string()
            case 3: replyTo = try r.string()
            case 4: updatedAt = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `DRAFTS` — a page plus cursor.
public struct DraftsBody: ProtoMessage, Sendable, Equatable {
    public var drafts: [DraftItemBody] = []
    public var cursor: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.repeatedMessage(1, drafts)
        w.int64(2, cursor)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: drafts.append(try r.message(DraftItemBody.self))
            case 2: cursor = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

// MARK: - Pins

/// `PIN` / `UNPIN` / `PIN_LIST` all carry this (`PinAction` in the schema). The
/// `Pin` message is the *item* inside a `PINNED` reply, not a request.
public struct PinActionBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var messageID = ""

    public init(chatID: String = "", messageID: String = "") {
        self.chatID = chatID
        self.messageID = messageID
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, messageID)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: messageID = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// One pinned message.
public struct PinBody: ProtoMessage, Sendable, Equatable {
    public var messageID = ""
    public var pinnedBy = ""
    public var pinnedAt: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, messageID)
        w.string(2, pinnedBy)
        w.int64(3, pinnedAt)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: messageID = try r.string()
            case 2: pinnedBy = try r.string()
            case 3: pinnedAt = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `PINNED` — the chat's full pin set, pushed whenever it changes.
public struct PinnedBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var pins: [PinBody] = []

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.repeatedMessage(2, pins)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: pins.append(try r.message(PinBody.self))
            default: try r.skip(f)
            }
        }
    }
}
