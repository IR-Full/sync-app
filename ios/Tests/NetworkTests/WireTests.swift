import XCTest
@testable import SynapseNetwork

/// Framing and envelope tests.
///
/// These assert against *byte layouts*, not against our own round trip. A
/// round-trip test would pass just as happily if both the encoder and the
/// decoder were wrong in the same way, which is precisely the failure mode when
/// reimplementing someone else's protocol.
final class FrameTests: XCTestCase {

    func testHeaderLayoutMatchesTheServer() throws {
        let payload = Data([0xAA, 0xBB, 0xCC])
        let frame = try Frame.encode(payload: payload)

        XCTAssertEqual(frame.count, 8 + 3)
        XCTAssertEqual(frame[0], 0x53, "magic must be 'S'")
        XCTAssertEqual(frame[1], 0x43, "magic must be 'C'")
        XCTAssertEqual(frame[2], 0x01, "framing version")
        XCTAssertEqual(frame[3], 0x00, "we never set a compression flag")
        // Length is big-endian (network byte order), not the host's.
        XCTAssertEqual(Array(frame[4..<8]), [0x00, 0x00, 0x00, 0x03])
        XCTAssertEqual(Array(frame[8...]), [0xAA, 0xBB, 0xCC])
    }

    func testRejectsForeignTraffic() {
        // A port scan, an HTTP request, anything that is not us.
        let garbage = Data("GET / HTTP/1.1\r\n".utf8)
        XCTAssertThrowsError(try Frame.decode(garbage)) { error in
            XCTAssertEqual(error as? WireError, .badMagic)
        }
    }

    func testRejectsUnknownVersion() {
        var frame = try! Frame.encode(payload: Data([1]))
        frame[2] = 0x02
        XCTAssertThrowsError(try Frame.decode(frame)) { error in
            XCTAssertEqual(error as? WireError, .unsupportedVersion(2))
        }
    }

    /// A hostile length prefix must be refused before anything is allocated —
    /// this is the check that stops a 4 GiB reservation from one bad frame.
    func testRejectsOversizedLengthPrefix() {
        var frame = Data([0x53, 0x43, 0x01, 0x00])
        frame.append(contentsOf: [0xFF, 0xFF, 0xFF, 0xFF])
        XCTAssertThrowsError(try Frame.decode(frame)) { error in
            XCTAssertEqual(error as? WireError, .payloadTooLarge)
        }
    }

    func testRejectsTruncatedPayload() {
        var frame = try! Frame.encode(payload: Data([1, 2, 3, 4]))
        frame.removeLast(2)
        XCTAssertThrowsError(try Frame.decode(frame)) { error in
            XCTAssertEqual(error as? WireError, .truncatedFrame)
        }
    }

    /// We never advertise compression, so a compressed frame is a protocol
    /// violation rather than something to silently mishandle.
    func testRejectsCompressedFrameWeNeverNegotiated() {
        var frame = try! Frame.encode(payload: Data([1, 2, 3]))
        frame[3] = FrameFlag.compressed
        XCTAssertThrowsError(try Frame.decode(frame)) { error in
            XCTAssertEqual(error as? WireError, .unsupportedCompression(FrameFlag.compressed))
        }
    }

    func testRoundTripsEmptyPayload() throws {
        let decoded = try Frame.decode(try Frame.encode(payload: Data()))
        XCTAssertTrue(decoded.isEmpty)
    }
}

final class VarintTests: XCTestCase {

    func testKnownEncodings() {
        func encoded(_ value: UInt64) -> [UInt8] {
            var data = Data()
            Varint.encode(value, into: &data)
            return [UInt8](data)
        }
        XCTAssertEqual(encoded(0), [0x00])
        XCTAssertEqual(encoded(1), [0x01])
        XCTAssertEqual(encoded(127), [0x7F])
        XCTAssertEqual(encoded(128), [0x80, 0x01])
        XCTAssertEqual(encoded(300), [0xAC, 0x02])
        XCTAssertEqual(encoded(UInt64.max), [0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01])
    }

    func testRoundTripsBoundaryValues() throws {
        for value in [UInt64(0), 1, 127, 128, 16383, 16384, UInt64(UInt32.max), UInt64.max] {
            var data = Data()
            Varint.encode(value, into: &data)
            var offset = 0
            XCTAssertEqual(try Varint.decode([UInt8](data), &offset), value)
            XCTAssertEqual(offset, data.count, "decode must consume exactly what encode wrote")
        }
    }

    /// An unterminated run of continuation bytes must not spin forever.
    func testRejectsRunawayVarint() {
        var offset = 0
        XCTAssertThrowsError(try Varint.decode([UInt8](repeating: 0x80, count: 32), &offset))
    }
}

final class EnvelopeTests: XCTestCase {

    func testFieldOrderMatchesTheServer() throws {
        let envelope = Envelope(type: .send, seq: 1, ack: 2, requestID: 3, body: Data([0x09]))
        let encoded = [UInt8](envelope.encode())
        // Type(8), Seq(1), Ack(2), RequestID(3), len(1), body — all uvarints.
        XCTAssertEqual(encoded, [8, 1, 2, 3, 1, 0x09])
    }

    func testRoundTrip() throws {
        let original = Envelope(
            type: .new, seq: 300, ack: 128, requestID: 65_535, body: Data(repeating: 0x7F, count: 200)
        )
        let decoded = try Envelope.decode(original.encode())
        XCTAssertEqual(decoded, original)
    }

    /// The extensibility rule: a type this build does not know must decode to
    /// `.unknown` and be skipped, never throw. A server that adds a message type
    /// must not break clients already in the field.
    func testUnknownTypeDecodesRatherThanThrows() throws {
        var data = Data()
        Varint.encode(9_999, into: &data)  // type
        Varint.encode(1, into: &data)      // seq
        Varint.encode(0, into: &data)      // ack
        Varint.encode(0, into: &data)      // requestID
        Varint.encode(0, into: &data)      // body length

        let decoded = try Envelope.decode(data)
        XCTAssertEqual(decoded.type, .unknown)
        XCTAssertEqual(decoded.seq, 1)
    }

    func testRejectsBodyLongerThanTheBuffer() {
        var data = Data()
        Varint.encode(8, into: &data)
        Varint.encode(0, into: &data)
        Varint.encode(0, into: &data)
        Varint.encode(0, into: &data)
        Varint.encode(50, into: &data)  // claims 50 bytes
        data.append(contentsOf: [1, 2, 3])

        XCTAssertThrowsError(try Envelope.decode(data)) { error in
            XCTAssertEqual(error as? WireError, .truncatedEnvelope)
        }
    }
}
