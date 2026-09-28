import Foundation

// Envelope bodies for the handshake, authentication, session resume, errors and
// push registration. Field numbers come from server/proto/syncapp/v1/body.proto
// and must never be renumbered independently of it.

/// `HELLO` — the first frame on every connection.
public struct HelloBody: ProtoMessage, Sendable, Equatable {
    public var clientVersion = ""
    public var deviceID = ""
    public var platform = ""        // ios | android | web | desktop | cli
    public var caps: Capabilities = []
    public var resumeToken = ""

    public init(clientVersion: String = "", deviceID: String = "", platform: String = "",
                caps: Capabilities = [], resumeToken: String = "") {
        self.clientVersion = clientVersion
        self.deviceID = deviceID
        self.platform = platform
        self.caps = caps
        self.resumeToken = resumeToken
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, clientVersion)
        w.string(2, deviceID)
        w.string(3, platform)
        w.uint32(4, caps.rawValue)
        w.string(5, resumeToken)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: clientVersion = try r.string()
            case 2: deviceID = try r.string()
            case 3: platform = try r.string()
            case 4: caps = Capabilities(rawValue: try r.uint32())
            case 5: resumeToken = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `WELCOME` — the negotiated connection parameters.
public struct WelcomeBody: ProtoMessage, Sendable, Equatable {
    public var serverVersion = ""
    public var sessionID = ""
    public var caps: Capabilities = []
    public var heartbeatMs: Int32 = 0
    public var maxInflight: Int32 = 0
    public var resumeSupported = false

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, serverVersion)
        w.string(2, sessionID)
        w.uint32(3, caps.rawValue)
        w.int32(4, heartbeatMs)
        w.int32(5, maxInflight)
        w.bool(6, resumeSupported)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: serverVersion = try r.string()
            case 2: sessionID = try r.string()
            case 3: caps = Capabilities(rawValue: try r.uint32())
            case 4: heartbeatMs = try r.int32()
            case 5: maxInflight = try r.int32()
            case 6: resumeSupported = try r.bool()
            default: try r.skip(f)
            }
        }
    }
}

/// `AUTH` — a bearer token, or credentials.
///
/// `register` is an explicit intent, not a fallback: the gateway never creates
/// an account on a failed login, which is what stops account-existence probing
/// and accidental takeover of a typo'd username.
public struct AuthBody: ProtoMessage, Sendable, Equatable {
    public var token = ""
    public var username = ""
    public var password = ""
    public var register = false
    /// Honoured on registration only. Afterwards `PROFILE_SET` is the single
    /// writer of the name, so a stale client cannot revert one changed elsewhere.
    public var displayName = ""
    /// The second factor, sent on the RETRY after the server answered
    /// `twoFactorRequired`.
    ///
    /// A client cannot know in advance whether an account enforces one — asking would
    /// make the protocol an oracle for which accounts are protected — so the flow is
    /// credentials, then "a code is needed", then both. A RECOVERY code is accepted
    /// here too: the user who lost their phone is looking at the same prompt, and
    /// making them find a different screen is how a recovery path goes unused.
    public var totpCode = ""

    public init(token: String) { self.token = token }
    public init(
        username: String,
        password: String,
        register: Bool,
        displayName: String = "",
        totpCode: String = ""
    ) {
        self.username = username
        self.password = password
        self.register = register
        self.displayName = displayName
        self.totpCode = totpCode
    }
    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, token)
        w.string(2, username)
        w.string(3, password)
        w.bool(4, register)
        w.string(5, displayName)
        w.string(6, totpCode)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: token = try r.string()
            case 2: username = try r.string()
            case 3: password = try r.string()
            case 4: register = try r.bool()
            case 5: displayName = try r.string()
            case 6: totpCode = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `AUTH_OK` — identity plus the two credentials worth keeping: a bearer token
/// for the next login and a resume token for the next dropped socket.
public struct AuthOKBody: ProtoMessage, Sendable, Equatable {
    public var userID = ""
    public var deviceID = ""
    public var sessionID = ""
    public var token = ""
    public var resumeToken = ""
    /// Who the session belongs to. A login by TOKEN — every launch after the
    /// first — sends no username, so without these the app would know its own id
    /// and nothing else about itself.
    public var username = ""
    public var displayName = ""
    public var avatarRef = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, userID)
        w.string(2, deviceID)
        w.string(3, sessionID)
        w.string(4, token)
        w.string(5, resumeToken)
        w.string(6, username)
        w.string(7, displayName)
        w.string(8, avatarRef)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: userID = try r.string()
            case 2: deviceID = try r.string()
            case 3: sessionID = try r.string()
            case 4: token = try r.string()
            case 5: resumeToken = try r.string()
            case 6: username = try r.string()
            case 7: displayName = try r.string()
            case 8: avatarRef = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `RESUME` — reattach to a dropped session and replay what we missed.
public struct ResumeBody: ProtoMessage, Sendable, Equatable {
    public var resumeToken = ""
    public var lastAckSeq: UInt64 = 0

    public init(resumeToken: String = "", lastAckSeq: UInt64 = 0) {
        self.resumeToken = resumeToken
        self.lastAckSeq = lastAckSeq
    }

    public func encode(to w: inout ProtoWriter) {
        w.string(1, resumeToken)
        w.uint64(2, lastAckSeq)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: resumeToken = try r.string()
            case 2: lastAckSeq = try r.uint64()
            default: try r.skip(f)
            }
        }
    }
}

/// `RESUME_OK` — replay of unacked frames precedes this frame.
public struct ResumeOKBody: ProtoMessage, Sendable, Equatable {
    public var sessionID = ""
    public var fromSeq: UInt64 = 0
    /// The token to use NEXT time, because resuming CONSUMES the one just sent.
    ///
    /// Keeping the old one is not a missed optimisation — it ends the session. The
    /// server remembers the consumed token precisely so that presenting it again is
    /// detectable, and treats that as theft: two parties holding one token, with no
    /// way to tell which is the owner, so it kills the chain. A client that stored the
    /// spent token therefore works for exactly one reconnect and then logs itself out.
    ///
    /// Empty from an older gateway that does not rotate, in which case the existing
    /// token is still current.
    public var resumeToken = ""

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.string(1, sessionID)
        w.uint64(2, fromSeq)
        w.string(3, resumeToken)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: sessionID = try r.string()
            case 2: fromSeq = try r.uint64()
            case 3: resumeToken = try r.string()
            default: try r.skip(f)
            }
        }
    }
}

/// `ERROR` / `AUTH_ERR`.
public struct ErrorBody: ProtoMessage, Sendable, Equatable {
    public var code: ErrorCode = .none
    public var message = ""
    public var retryAfterMs: Int32 = 0

    public init() {}

    public func encode(to w: inout ProtoWriter) {
        w.uint32(1, code.rawValue)
        w.string(2, message)
        w.int32(3, retryAfterMs)
    }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: code = ErrorCode(rawValue: try r.uint32()) ?? .unknown
            case 2: message = try r.string()
            case 3: retryAfterMs = try r.int32()
            default: try r.skip(f)
            }
        }
    }

    public var asProtocolError: ProtocolError {
        ProtocolError(code: code, message: message, retryAfterMs: Int(retryAfterMs))
    }
}

/// `PUSH_TOKEN` — register or refresh this device's APNs token. An empty token
/// clears it, which is how "turn notifications off" stops them at the source
/// rather than at the device.
public struct PushTokenBody: ProtoMessage, Sendable, Equatable {
    public var token = ""

    public init(token: String = "") { self.token = token }

    public func encode(to w: inout ProtoWriter) { w.string(1, token) }

    public init(from r: inout ProtoReader) throws {
        self.init()
        while let f = try r.next() {
            switch f.number {
            case 1: token = try r.string()
            default: try r.skip(f)
            }
        }
    }
}
