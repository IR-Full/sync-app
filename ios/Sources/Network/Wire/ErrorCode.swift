import Foundation

/// Machine-readable error identifiers carried in `ERROR` / `AUTH_ERR` bodies.
///
/// The server groups codes into decimal ranges so a client can react by *class*
/// even when it has never seen the specific code — which is exactly what
/// `isRetryable` and `isAuthFailure` below do. That is the whole reason the
/// ranges exist, so we honour them instead of switching on every case.
public enum ErrorCode: UInt32, Sendable {
    case none = 0

    // 1xxx — transport / protocol.
    case protocolViolation = 1000
    case badFrame = 1001
    case unsupported = 1002
    case payloadTooBig = 1003
    case resumeExpired = 1004

    // 2xxx — auth / session: the client must re-authenticate.
    case unauthenticated = 2000
    case badToken = 2001
    case sessionRevoked = 2002
    case deviceUnknown = 2003

    // 3xxx — authorization / business: do not retry as-is.
    case forbidden = 3000
    case notFound = 3001
    case conflict = 3002
    case badArgument = 3003

    // 4xxx — throttling: retry after `retryAfterMs`.
    case rateLimited = 4000
    case flood = 4001

    // 5xxx — server: retry with backoff (writes are idempotent via dedup key).
    case internalError = 5000
    case unavailable = 5001

    case unknown = 0xFFFF_FFFF

    /// 2xxx means the session itself is dead — reconnecting cannot fix it, only
    /// a fresh login can. `resumeExpired` is deliberately *not* in this set: it
    /// means the replay buffer aged out, and a full re-AUTH with the bearer
    /// token we still hold recovers cleanly.
    public var isAuthFailure: Bool { (2000..<3000).contains(rawValue) }

    /// 1xxx (fix framing and retry), 4xxx (back off), 5xxx (server-side).
    public var isRetryable: Bool {
        (1000..<2000).contains(rawValue) || (4000..<5000).contains(rawValue) || rawValue >= 5000
    }
}

/// A business/protocol error the server reported for one of our requests.
public struct ProtocolError: Error, Equatable, Sendable {
    public let code: ErrorCode
    public let message: String
    public let retryAfterMs: Int

    public init(code: ErrorCode, message: String, retryAfterMs: Int = 0) {
        self.code = code
        self.message = message
        self.retryAfterMs = retryAfterMs
    }

    public var isAuthFailure: Bool { code.isAuthFailure }
    public var isRetryable: Bool { code.isRetryable }
}

extension ProtocolError: LocalizedError {
    public var errorDescription: String? { message.isEmpty ? "\(code)" : message }
}

/// Client-side failures that never reached, or never came back from, the server.
public enum ClientError: Error, Equatable, Sendable {
    case notConnected
    case requestTimedOut(MsgType)
    case unexpectedReply(expected: MsgType, got: MsgType)
    case handshakeFailed(String)
    case noCredentials
    case closed
}
