import Foundation

/// The frame payload: a varint-packed routing header plus an opaque body.
///
/// Field order on the wire (all LEB128 unsigned varints), per
/// `server/pkg/wire/envelope.go`:
///
///     Type, Seq, Ack, RequestID, len(Body), Body
///
/// - `seq` is our own per-connection monotonic counter. It resets on every new
///   socket, because the server's view of it resets too.
/// - `ack` piggybacks the highest *server* seq we have processed, which is what
///   lets the gateway trim its replay buffer without a dedicated ack frame.
/// - `requestID` correlates a reply to its request and is independent of
///   ordering, so many requests can be in flight over the one connection.
///   `0` means "not correlated" — i.e. an unsolicited server push.
public struct Envelope: Equatable, Sendable {
    public var type: MsgType
    public var seq: UInt64
    public var ack: UInt64
    public var requestID: UInt64
    public var body: Data

    public init(type: MsgType, seq: UInt64 = 0, ack: UInt64 = 0, requestID: UInt64 = 0, body: Data = Data()) {
        self.type = type
        self.seq = seq
        self.ack = ack
        self.requestID = requestID
        self.body = body
    }

    public func encode() -> Data {
        var out = Data(capacity: 16 + body.count)
        Varint.encode(UInt64(type.rawValue), into: &out)
        Varint.encode(seq, into: &out)
        Varint.encode(ack, into: &out)
        Varint.encode(requestID, into: &out)
        Varint.encode(UInt64(body.count), into: &out)
        out.append(body)
        return out
    }

    public static func decode(_ data: Data) throws -> Envelope {
        let bytes = [UInt8](data)
        var offset = 0
        let rawType = try Varint.decode(bytes, &offset)
        let seq = try Varint.decode(bytes, &offset)
        let ack = try Varint.decode(bytes, &offset)
        let requestID = try Varint.decode(bytes, &offset)
        let bodyLength = try Varint.decode(bytes, &offset)
        guard bodyLength <= UInt64(bytes.count - offset) else { throw WireError.truncatedEnvelope }

        // An unknown type is not an error: the protocol's extensibility rule is
        // that a peer which does not understand a type skips it. We keep the raw
        // value so the client can log it and move on.
        let type = MsgType(rawValue: UInt16(truncatingIfNeeded: rawType)) ?? .unknown
        let body = bodyLength == 0 ? Data() : Data(bytes[offset..<(offset + Int(bodyLength))])
        return Envelope(type: type, seq: seq, ack: ack, requestID: requestID, body: body)
    }
}
