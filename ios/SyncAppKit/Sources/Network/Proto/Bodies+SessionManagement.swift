import Foundation

// Envelope bodies for session management and account erasure.
//
// Field numbers come from server/proto/syncapp/v1/body.proto and must never be
// renumbered independently of it: the number is the only thing protobuf puts on
// the wire, so two swapped ones produce a frame that both sides encode and
// decode happily and that means something else entirely.
//
// These are the last part of the protocol this platform did not speak. The auth
// service could always list and revoke sessions, and nothing could ask it to —
// which made "log out" a purely local gesture: the device forgot its token while
// the session stayed valid on the server until it expired, so a lost phone kept
// access for the whole TTL. Account deletion was in the same position.

// MARK: - Session management

/// `SESSION_LIST` — every live session of the CALLER's account.
///
/// No fields, and that is the design: the account is the authenticated
/// connection, and letting a client name a different one would turn this into a
/// cross-account enumeration primitive.
public struct SessionListBody: ProtoMessage, Sendable, Equatable {
    public init() {}

    public func encode(to w: inout ProtoWriter) {}

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() { try r.skip(f) }
    }
}

/// One live session, as shown in the device list.
///
/// It deliberately carries NO token. The point of the list is to let a person
/// recognise a device well enough to decide whether to kill it; handing every
/// device the credentials of every other would turn a read into a
/// lateral-movement tool, which is the opposite of what this is for.
public struct SessionInfoBody: ProtoMessage, Sendable, Equatable {
    public var sessionID = ""
    public var deviceID = ""
    public var platform = ""
    public var createdAt: Int64 = 0
    public var expiresAt: Int64 = 0
    /// Marks the session this connection authenticated with, so the UI can label
    /// it and warn before revoking it.
    public var current = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, sessionID)
        w.string(2, deviceID)
        w.string(3, platform)
        w.int64(4, createdAt)
        w.int64(5, expiresAt)
        w.bool(6, current)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: sessionID = try r.string()
            case 2: deviceID = try r.string()
            case 3: platform = try r.string()
            case 4: createdAt = try r.int64()
            case 5: expiresAt = try r.int64()
            case 6: current = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `SESSIONS` — the answer to `SESSION_LIST`.
public struct SessionsBody: ProtoMessage, Sendable, Equatable {
    public var sessions: [SessionInfoBody] = []

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.repeatedMessage(1, sessions)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: sessions.append(try r.message(SessionInfoBody.self))
            default: try r.skip(f)
            }
        }
    }
}

/// `SESSION_REVOKE` — kill sessions.
///
/// An empty `sessionID` means "every session except this one" — the "sign out
/// everywhere else" a person reaches for after losing a device.
/// `allIncludingCurrent` extends the sweep to this connection too; it is a
/// separate field because "log out everywhere, including here" and "log out
/// everywhere but here" are different intentions, and a client should not have
/// to express the difference by omitting something.
public struct SessionRevokeBody: ProtoMessage, Sendable, Equatable {
    public var sessionID = ""
    public var allIncludingCurrent = false

    public init(sessionID: String = "", allIncludingCurrent: Bool = false) {
        self.sessionID = sessionID
        self.allIncludingCurrent = allIncludingCurrent
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, sessionID)
        w.bool(2, allIncludingCurrent)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: sessionID = try r.string()
            case 2: allIncludingCurrent = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `SESSION_REVOKED` — what the revoke actually did.
///
/// The count matters: a client that asked to sign out five devices and signed
/// out one should say so rather than show a checkmark.
public struct SessionRevokedBody: ProtoMessage, Sendable, Equatable {
    public var revoked: Int32 = 0
    /// True when the caller's own session was among those killed, so the app
    /// drops its stored credentials instead of waiting for the socket to fail.
    ///
    /// `isSelf` rather than `self`, which is the name the field has in the proto
    /// and in the Go struct. A property called `self` is reachable in Swift only
    /// through backticks at the declaration, and `reply.self` does not read it at
    /// all — `.self` is the postfix expression that yields the value itself, so
    /// the compiler accepts it and hands back the whole body instead of the flag.
    /// The wire is unaffected: protobuf carries field number 2, never a name.
    public var isSelf = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.int32(1, revoked)
        w.bool(2, isSelf)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: revoked = try r.int32()
            case 2: isSelf = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

// MARK: - Account erasure

/// `ACCOUNT_DELETE` — erase the caller's account.
///
/// The password is re-confirmed even though the socket is already
/// authenticated, and that is not ceremony: a session token lives on the device,
/// so without it anyone holding an unlocked phone could destroy the account
/// behind it.
public struct AccountDeleteBody: ProtoMessage, Sendable, Equatable {
    public var password = ""
    /// Optional free text. It reaches the audit log and nowhere else — in
    /// particular it never lands on a row that survives the deletion.
    public var reason = ""

    public init(password: String = "", reason: String = "") {
        self.password = password
        self.reason = reason
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, password)
        w.string(2, reason)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: password = try r.string()
            case 2: reason = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `ACCOUNT_DELETED` — the erasure is done.
///
/// Every session is revoked before this is sent, so it is the last frame the
/// connection will ever carry.
public struct AccountDeletedBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var deletedAt: Int64 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.int64(2, deletedAt)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: deletedAt = try r.int64()
            default: try r.skip(f)
            }
        }
    }
}
