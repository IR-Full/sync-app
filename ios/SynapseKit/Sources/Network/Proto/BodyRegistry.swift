import Foundation

/// Which protobuf body carries each envelope type.
///
/// Derived from the gateway's handlers, not from the type names — two pairings
/// are not guessable: `PIN`/`UNPIN`/`PIN_LIST` all carry `PinAction` (the `Pin`
/// message is the item inside a `PINNED` reply), and `KEY_FETCH_ALL` reuses
/// `KeyFetch`. Types absent from this table carry no body at all
/// (`PING`, `PONG`, `T_ACK`) and decode to `nil` rather than failing.
public enum BodyRegistry {

    /// Decodes an envelope's body into a concrete type, or returns nil when the
    /// type is bodiless / empty.
    public static func decode<T: ProtoDecodable>(_ type: T.Type, from envelope: Envelope) throws -> T {
        try T.protoDecoded(from: envelope.body)
    }

    /// A type-erased decode used by the logging/debug path and by the client's
    /// push dispatcher, which needs to know whether a body is worth parsing.
    public static func hasBody(_ type: MsgType) -> Bool {
        switch type {
        case .ping, .pong, .transportAck, .unknown:
            return false
        default:
            return true
        }
    }
}

/// A decoded reply: the envelope metadata plus its typed body.
public struct Reply<Body: Sendable>: Sendable {
    public let type: MsgType
    public let seq: UInt64
    public let requestID: UInt64
    public let body: Body

    public init(type: MsgType, seq: UInt64, requestID: UInt64, body: Body) {
        self.type = type
        self.seq = seq
        self.requestID = requestID
        self.body = body
    }
}
