import Foundation

// Message-layer bodies: send/ack/deliver, receipts, typing, presence, edit,
// delete, history paging, reactions, threads, forwarding.

/// Typed media metadata attached to a message. The bytes went through the media
/// HTTP pipeline first; this only describes them, which is what lets a client
/// render a voice waveform or a file card without downloading the blob.
public struct AttachmentBody: ProtoMessage, Sendable, Equatable {
    public var kind = ""            // voice | video_note | file | image | video
    public var mediaRef = ""
    public var filename = ""
    public var mime = ""
    public var size: Int64 = 0
    public var durationMs: Int64 = 0
    public var waveform: [Int32] = []
    public var width: Int32 = 0
    public var height: Int32 = 0
    public var thumbRef = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, kind)
        w.string(2, mediaRef)
        w.string(3, filename)
        w.string(4, mime)
        w.int64(5, size)
        w.int64(6, durationMs)
        w.packedInt32(7, waveform)
        w.int32(8, width)
        w.int32(9, height)
        w.string(10, thumbRef)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: kind = try r.string()
            case 2: mediaRef = try r.string()
            case 3: filename = try r.string()
            case 4: mime = try r.string()
            case 5: size = try r.int64()
            case 6: durationMs = try r.int64()
            case 7: try r.repeatedInt32(f, into: &waveform)
            case 8: width = try r.int32()
            case 9: height = try r.int32()
            case 10: thumbRef = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// Provenance that travels with a forwarded message. A snapshot, not a live
/// reference — it survives the original being deleted.
public struct ForwardOriginBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var messageID = ""
    public var senderID = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, messageID)
        w.string(3, senderID)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: messageID = try r.string()
            case 3: senderID = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `SEND`.
///
/// `chatID` accepts either a snowflake or `"@username"`; the gateway resolves
/// the latter to the canonical 1:1 chat (creating it if needed), which is how a
/// client starts a direct conversation without knowing a chat id.
///
/// `dedupKey` is a client-generated idempotency key. The server maps
/// (device, dedupKey) → message id, so retrying a send whose ack we lost returns
/// the original message with `duplicate = true` instead of posting twice. This
/// is the single most important field for the offline outbox.
public struct SendBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var dedupKey = ""
    public var text = ""
    public var mediaRef = ""
    public var replyTo = ""
    public var attachment: AttachmentBody?
    public var ttlSeconds: Int32 = 0

    public init(chatID: String = "", dedupKey: String = "", text: String = "",
                mediaRef: String = "", replyTo: String = "",
                attachment: AttachmentBody? = nil, ttlSeconds: Int32 = 0) {
        self.chatID = chatID
        self.dedupKey = dedupKey
        self.text = text
        self.mediaRef = mediaRef
        self.replyTo = replyTo
        self.attachment = attachment
        self.ttlSeconds = ttlSeconds
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, dedupKey)
        w.string(3, text)
        w.string(4, mediaRef)
        w.string(5, replyTo)
        w.message(6, attachment)
        w.int32(7, ttlSeconds)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: dedupKey = try r.string()
            case 3: text = try r.string()
            case 4: mediaRef = try r.string()
            case 5: replyTo = try r.string()
            case 6: attachment = try r.message(AttachmentBody.self)
            case 7: ttlSeconds = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `SEND_ACK` — durable persistence confirmed, with the ordering metadata.
public struct SendAckBody: ProtoMessage, Sendable, Equatable {
    public var dedupKey = ""
    public var messageID = ""
    public var chatID = ""
    public var chatSeq: UInt64 = 0
    public var timestamp: Int64 = 0
    public var duplicate = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, dedupKey)
        w.string(2, messageID)
        w.string(3, chatID)
        w.uint64(4, chatSeq)
        w.int64(5, timestamp)
        w.bool(6, duplicate)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: dedupKey = try r.string()
            case 2: messageID = try r.string()
            case 3: chatID = try r.string()
            case 4: chatSeq = try r.uint64()
            case 5: timestamp = try r.int64()
            case 6: duplicate = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `NEW` — a message delivered to this device.
///
/// The same body serves live fanout *and* history replay; the only difference is
/// the envelope's `requestID` (0 for live, our request id for a history page).
public struct NewMessageBody: ProtoMessage, Sendable, Equatable {
    public var messageID = ""
    public var chatID = ""
    public var senderID = ""
    public var chatSeq: UInt64 = 0
    public var text = ""
    public var mediaRef = ""
    public var replyTo = ""
    public var edited = false
    public var deleted = false
    public var timestamp: Int64 = 0
    public var attachment: AttachmentBody?
    public var threadRoot = ""
    public var replyCount: Int32 = 0
    public var forward: ForwardOriginBody?
    public var expiresAt: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, messageID)
        w.string(2, chatID)
        w.string(3, senderID)
        w.uint64(4, chatSeq)
        w.string(5, text)
        w.string(6, mediaRef)
        w.string(7, replyTo)
        w.bool(8, edited)
        w.bool(9, deleted)
        w.int64(10, timestamp)
        w.message(11, attachment)
        w.string(12, threadRoot)
        w.int32(13, replyCount)
        w.message(14, forward)
        w.int64(15, expiresAt)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: messageID = try r.string()
            case 2: chatID = try r.string()
            case 3: senderID = try r.string()
            case 4: chatSeq = try r.uint64()
            case 5: text = try r.string()
            case 6: mediaRef = try r.string()
            case 7: replyTo = try r.string()
            case 8: edited = try r.bool()
            case 9: deleted = try r.bool()
            case 10: timestamp = try r.int64()
            case 11: attachment = try r.message(AttachmentBody.self)
            case 12: threadRoot = try r.string()
            case 13: replyCount = try r.int32()
            case 14: forward = try r.message(ForwardOriginBody.self)
            case 15: expiresAt = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `READ` — mark a chat read up to a position. Answered only on failure.
public struct ReadBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var upToMessageID = ""
    public var upToChatSeq: UInt64 = 0

    public init(chatID: String = "", upToMessageID: String = "", upToChatSeq: UInt64 = 0) {
        self.chatID = chatID
        self.upToMessageID = upToMessageID
        self.upToChatSeq = upToChatSeq
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, upToMessageID)
        w.uint64(3, upToChatSeq)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: upToMessageID = try r.string()
            case 3: upToChatSeq = try r.uint64()
            default: try r.skip(f)
            }
        }
    }
}

/// `READ_UPD` — someone else read up to a position.
public struct ReadUpdateBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var userID = ""
    public var upToChatSeq: UInt64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, userID)
        w.uint64(3, upToChatSeq)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: userID = try r.string()
            case 3: upToChatSeq = try r.uint64()
            default: try r.skip(f)
            }
        }
    }
}

/// `TYPING`. `userID` is empty outbound and stamped by the server inbound.
public struct TypingBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var userID = ""
    public var active = false

    public init(chatID: String = "", userID: String = "", active: Bool = false) {
        self.chatID = chatID
        self.userID = userID
        self.active = active
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, userID)
        w.bool(3, active)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: userID = try r.string()
            case 3: active = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `PRESENCE` — online / last-seen.
public struct PresenceBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var online = false
    public var lastSeenMs: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.bool(2, online)
        w.int64(3, lastSeenMs)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: online = try r.bool()
            case 3: lastSeenMs = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `EDIT`.
public struct EditBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var messageID = ""
    public var text = ""

    public init(chatID: String = "", messageID: String = "", text: String = "") {
        self.chatID = chatID
        self.messageID = messageID
        self.text = text
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, messageID)
        w.string(3, text)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: messageID = try r.string()
            case 3: text = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `DELETE` — a tombstone; the server keeps an audit copy.
public struct DeleteBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var messageID = ""
    public var forAll = false

    public init(chatID: String = "", messageID: String = "", forAll: Bool = false) {
        self.chatID = chatID
        self.messageID = messageID
        self.forAll = forAll
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, messageID)
        w.bool(3, forAll)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: messageID = try r.string()
            case 3: forAll = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `HISTORY` — backfill before a cursor. `beforeSeq == 0` means "latest".
public struct HistoryBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var beforeSeq: UInt64 = 0
    public var limit: Int32 = 0

    public init(chatID: String = "", beforeSeq: UInt64 = 0, limit: Int32 = 0) {
        self.chatID = chatID
        self.beforeSeq = beforeSeq
        self.limit = limit
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.uint64(2, beforeSeq)
        w.int32(3, limit)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: beforeSeq = try r.uint64()
            case 3: limit = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `HISTORY_OK` — terminates a history page and carries the next cursor.
public struct HistoryOKBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var nextBefore: UInt64 = 0
    public var done = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.uint64(2, nextBefore)
        w.bool(3, done)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: nextBefore = try r.uint64()
            case 3: done = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `REACT` — toggle. Sending the emoji you already have removes it.
public struct ReactBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var messageID = ""
    public var emoji = ""

    public init(chatID: String = "", messageID: String = "", emoji: String = "") {
        self.chatID = chatID
        self.messageID = messageID
        self.emoji = emoji
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, messageID)
        w.string(3, emoji)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: messageID = try r.string()
            case 3: emoji = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `REACT_UPD` — carries the full post-change tally so a client renders without
/// re-fetching.
public struct ReactUpdateBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var messageID = ""
    public var userID = ""
    public var emoji = ""
    public var added = false
    public var counts: [String: Int32] = [:]

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, messageID)
        w.string(3, userID)
        w.string(4, emoji)
        w.bool(5, added)
        w.stringInt32Map(6, counts)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: messageID = try r.string()
            case 3: userID = try r.string()
            case 4: emoji = try r.string()
            case 5: added = try r.bool()
            case 6: try r.stringInt32MapEntry(into: &counts)
            default: try r.skip(f)
            }
        }
    }
}

/// `THREAD` — replies under a root, oldest first (forward paging).
public struct ThreadBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var rootID = ""
    public var afterSeq: UInt64 = 0
    public var limit: Int32 = 0

    public init(chatID: String = "", rootID: String = "", afterSeq: UInt64 = 0, limit: Int32 = 0) {
        self.chatID = chatID
        self.rootID = rootID
        self.afterSeq = afterSeq
        self.limit = limit
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, rootID)
        w.uint64(3, afterSeq)
        w.int32(4, limit)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: rootID = try r.string()
            case 3: afterSeq = try r.uint64()
            case 4: limit = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// `THREAD_OK`.
public struct ThreadOKBody: ProtoMessage, Sendable, Equatable {
    public var chatID = ""
    public var rootID = ""
    public var nextAfter: UInt64 = 0
    public var done = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, chatID)
        w.string(2, rootID)
        w.uint64(3, nextAfter)
        w.bool(4, done)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: chatID = try r.string()
            case 2: rootID = try r.string()
            case 3: nextAfter = try r.uint64()
            case 4: done = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `FORWARD` — copy a message into another chat, keeping provenance. Answered
/// with a `SEND_ACK` for the new copy.
public struct ForwardBody: ProtoMessage, Sendable, Equatable {
    public var fromChatID = ""
    public var messageID = ""
    public var toChatID = ""
    public var dedupKey = ""

    public init(fromChatID: String = "", messageID: String = "", toChatID: String = "", dedupKey: String = "") {
        self.fromChatID = fromChatID
        self.messageID = messageID
        self.toChatID = toChatID
        self.dedupKey = dedupKey
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, fromChatID)
        w.string(2, messageID)
        w.string(3, toChatID)
        w.string(4, dedupKey)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: fromChatID = try r.string()
            case 2: messageID = try r.string()
            case 3: toChatID = try r.string()
            case 4: dedupKey = try r.string()
            default: try r.skip(f)
            }
        }
    }
}
