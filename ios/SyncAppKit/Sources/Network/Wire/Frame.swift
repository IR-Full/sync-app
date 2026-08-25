import Foundation

/// Errors raised by the framing / envelope parsers.
///
/// These are protocol-level faults, not business errors — a business error
/// arrives as a well-formed `ERROR` envelope and becomes a `ProtocolError`.
public enum WireError: Error, Equatable, Sendable {
    case badMagic
    case unsupportedVersion(UInt8)
    case payloadTooLarge
    case truncatedFrame
    case truncatedEnvelope
    /// A compressed frame arrived even though we never advertised compression.
    /// See `ClientCapabilities` for why we do not negotiate it.
    case unsupportedCompression(UInt8)
    case malformedBody(String)
}

/// Frame flag bits (`server/pkg/wire/frame.constants.go`).
public enum FrameFlag {
    public static let compressed: UInt8 = 1 << 0  // gzip
    public static let zstd: UInt8 = 1 << 1        // zstd + shared dictionary
}

/// The transport frame: a fixed 8-byte header followed by the envelope bytes.
///
///     +--------+--------+--------+--------+--------------------+============+
///     | 'S'    | 'C'    | VER(1) | FLAGS  | LENGTH (4, BE)     |  PAYLOAD   |
///     +--------+--------+--------+--------+--------------------+============+
///
/// The magic is a cheap sync word so the gateway can reject port scans without
/// allocating, and `LENGTH` is bounded so a hostile length prefix cannot make us
/// reserve 4 GiB. Both checks are reproduced here because a client parsing a
/// hostile *server* is the mirror image of the same problem.
public enum Frame {
    public static let magic0: UInt8 = 0x53  // 'S'
    public static let magic1: UInt8 = 0x43  // 'C'
    public static let version: UInt8 = 0x01
    public static let headerSize = 8
    public static let maxPayloadSize = 16 << 20  // 16 MiB

    /// Wraps an envelope payload in a frame. We never compress outbound frames:
    /// the server accepts uncompressed frames unconditionally.
    public static func encode(payload: Data, flags: UInt8 = 0) throws -> Data {
        guard payload.count <= maxPayloadSize else { throw WireError.payloadTooLarge }
        var out = Data(capacity: headerSize + payload.count)
        out.append(magic0)
        out.append(magic1)
        out.append(version)
        out.append(flags)
        let n = UInt32(payload.count)
        out.append(UInt8(truncatingIfNeeded: n >> 24))
        out.append(UInt8(truncatingIfNeeded: n >> 16))
        out.append(UInt8(truncatingIfNeeded: n >> 8))
        out.append(UInt8(truncatingIfNeeded: n))
        out.append(payload)
        return out
    }

    /// Parses exactly one complete in-memory frame and returns its payload.
    ///
    /// This is the WebSocket shape of the protocol — one binary message carries
    /// exactly one frame — which is also why there is no partial-read path here.
    public static func decode(_ data: Data) throws -> Data {
        let bytes = [UInt8](data)
        guard bytes.count >= headerSize else { throw WireError.truncatedFrame }
        guard bytes[0] == magic0, bytes[1] == magic1 else { throw WireError.badMagic }
        guard bytes[2] == version else { throw WireError.unsupportedVersion(bytes[2]) }

        let flags = bytes[3]
        let length =
            (UInt32(bytes[4]) << 24) | (UInt32(bytes[5]) << 16)
            | (UInt32(bytes[6]) << 8) | UInt32(bytes[7])
        guard Int(length) <= maxPayloadSize else { throw WireError.payloadTooLarge }
        guard bytes.count >= headerSize + Int(length) else { throw WireError.truncatedFrame }

        if flags & (FrameFlag.compressed | FrameFlag.zstd) != 0 {
            throw WireError.unsupportedCompression(flags)
        }
        return Data(bytes[headerSize..<(headerSize + Int(length))])
    }

    /// Reads the declared payload length out of a header, for the stream
    /// (TCP/QUIC) path where the payload arrives separately from the header.
    public static func parseHeader(_ header: [UInt8]) throws -> (flags: UInt8, length: Int) {
        guard header.count >= headerSize else { throw WireError.truncatedFrame }
        guard header[0] == magic0, header[1] == magic1 else { throw WireError.badMagic }
        guard header[2] == version else { throw WireError.unsupportedVersion(header[2]) }
        let length =
            (UInt32(header[4]) << 24) | (UInt32(header[5]) << 16)
            | (UInt32(header[6]) << 8) | UInt32(header[7])
        guard Int(length) <= maxPayloadSize else { throw WireError.payloadTooLarge }
        return (header[3], Int(length))
    }
}
