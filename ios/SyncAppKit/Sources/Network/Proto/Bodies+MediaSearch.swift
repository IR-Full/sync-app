import Foundation

// Media tickets and full-text search.
//
// Media bytes never travel as protocol frames — the binary protocol carries a
// short `media_ref` and the HTTP pipeline carries the blob, behind HMAC-signed,
// expiring URLs minted by these two round trips.

/// `MEDIA_INIT` — declare an upload, get a ticket.
public struct MediaInitBody: ProtoMessage, Sendable, Equatable {
    public var filename = ""
    public var contentType = ""
    public var size: Int64 = 0

    public init(filename: String = "", contentType: String = "", size: Int64 = 0) {
        self.filename = filename
        self.contentType = contentType
        self.size = size
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, filename)
        w.string(2, contentType)
        w.int64(3, size)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: filename = try r.string()
            case 2: contentType = try r.string()
            case 3: size = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `MEDIA_TICKET` — where to PUT the bytes, and the ref to attach to a message.
/// The signed size is binding: the upload must be exactly `size` bytes.
public struct MediaTicketBody: ProtoMessage, Sendable, Equatable {
    public var mediaRef = ""
    public var uploadURL = ""
    public var expiresAt: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, mediaRef)
        w.string(2, uploadURL)
        w.int64(3, expiresAt)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: mediaRef = try r.string()
            case 2: uploadURL = try r.string()
            case 3: expiresAt = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `MEDIA_FETCH`.
public struct MediaFetchBody: ProtoMessage, Sendable, Equatable {
    public var mediaRef = ""
    public init(mediaRef: String = "") { self.mediaRef = mediaRef }
    public func encode(to w: inout ProtoWriter) { w.string(1, mediaRef) }
    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: mediaRef = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `MEDIA_URL` — a signed, expiring download URL.
public struct MediaURLBody: ProtoMessage, Sendable, Equatable {
    public var mediaRef = ""
    public var downloadURL = ""
    public var expiresAt: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, mediaRef)
        w.string(2, downloadURL)
        w.int64(3, expiresAt)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: mediaRef = try r.string()
            case 2: downloadURL = try r.string()
            case 3: expiresAt = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}

/// `SEARCH` — full-text over the user's own chats. The server caps `limit` at 50
/// and defaults it to 20.
public struct SearchBody: ProtoMessage, Sendable, Equatable {
    public var query = ""
    public var limit: Int32 = 0

    public init(query: String = "", limit: Int32 = 0) {
        self.query = query
        self.limit = limit
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, query)
        w.int32(2, limit)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: query = try r.string()
            case 2: limit = try r.int32()
            default: try r.skip(f)
            }
        }
    }
}

/// One search hit.
public struct SearchHitBody: ProtoMessage, Sendable, Equatable {
    public var messageID = ""
    public var chatID = ""
    public var senderID = ""
    public var seq: UInt64 = 0
    public var text = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, messageID)
        w.string(2, chatID)
        w.string(3, senderID)
        w.uint64(4, seq)
        w.string(5, text)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: messageID = try r.string()
            case 2: chatID = try r.string()
            case 3: senderID = try r.string()
            case 4: seq = try r.uint64()
            case 5: text = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `SEARCH_RESULTS` — ranked and permission-filtered server-side.
public struct SearchResultsBody: ProtoMessage, Sendable, Equatable {
    public var query = ""
    public var hits: [SearchHitBody] = []

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, query)
        w.repeatedMessage(2, hits)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: query = try r.string()
            case 2: hits.append(try r.message(SearchHitBody.self))
            default: try r.skip(f)
            }
        }
    }
}
