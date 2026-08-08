import Foundation

/// LEB128 unsigned varints — the encoding of every envelope header field, and
/// of every protobuf tag and scalar inside a body.
public enum Varint {
    /// Appends `value` to `out` as an unsigned LEB128 varint.
    public static func encode(_ value: UInt64, into out: inout Data) {
        var v = value
        while v >= 0x80 {
            out.append(UInt8(truncatingIfNeeded: v) | 0x80)
            v >>= 7
        }
        out.append(UInt8(truncatingIfNeeded: v))
    }

    /// Decodes a varint at `offset`, advancing it past the bytes consumed.
    ///
    /// Rejects anything longer than 10 bytes: a 64-bit value cannot need more,
    /// and without the cap a run of `0x80` bytes is an unbounded parse loop.
    public static func decode(_ bytes: [UInt8], _ offset: inout Int) throws -> UInt64 {
        var result: UInt64 = 0
        var shift: UInt64 = 0
        var consumed = 0
        while offset < bytes.count {
            let byte = bytes[offset]
            offset += 1
            consumed += 1
            result |= UInt64(byte & 0x7F) << shift
            if byte & 0x80 == 0 { return result }
            shift += 7
            if consumed >= 10 { throw WireError.truncatedEnvelope }
        }
        throw WireError.truncatedEnvelope
    }
}
