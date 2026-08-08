import XCTest
@testable import SynapseNetwork

/// Protobuf body tests.
///
/// The golden-byte cases matter more than the round trips: they are what catch a
/// field number typed one off from `body.proto`, which a round trip cannot see
/// because both sides would share the mistake.
final class ProtoCodecTests: XCTestCase {

    func testScalarEncodingMatchesProto3() {
        var writer = ProtoWriter()
        writer.string(1, "hi")     // tag 0x0A, len 2
        writer.uint64(4, 300)      // tag 0x20, varint 0xAC 0x02
        writer.bool(6, true)       // tag 0x30, varint 1
        XCTAssertEqual([UInt8](writer.data), [0x0A, 0x02, 0x68, 0x69, 0x20, 0xAC, 0x02, 0x30, 0x01])
    }

    /// proto3 has implicit presence: a default-valued field is not written at
    /// all. Emitting zeros instead would still decode correctly but would inflate
    /// every frame — and would disagree with the Go encoder byte for byte.
    func testDefaultValuesAreOmitted() {
        var writer = ProtoWriter()
        writer.string(1, "")
        writer.uint64(2, 0)
        writer.bool(3, false)
        writer.int32(4, 0)
        XCTAssertTrue(writer.data.isEmpty)
    }

    func testUnknownFieldsAreSkippedNotRejected() throws {
        // A future server adds field 99 (a string) to SendAck.
        var writer = ProtoWriter()
        writer.string(2, "msg-1")
        writer.uint64(4, 42)
        writer.string(99, "something new")

        let decoded = try SendAckBody.protoDecoded(from: writer.data)
        XCTAssertEqual(decoded.messageID, "msg-1")
        XCTAssertEqual(decoded.chatSeq, 42)
    }

    func testHelloRoundTrip() throws {
        let hello = HelloBody(
            clientVersion: "ios/1.0",
            deviceID: "device-abc",
            platform: "ios",
            caps: [.resume, .typingSignals],
            resumeToken: ""
        )
        let decoded = try HelloBody.protoDecoded(from: hello.protoEncoded())
        XCTAssertEqual(decoded, hello)
        XCTAssertTrue(decoded.caps.contains(.resume))
        XCTAssertFalse(decoded.caps.contains(.compression))
    }

    func testNewMessageRoundTripIncludingNestedMessages() throws {
        var attachment = AttachmentBody()
        attachment.kind = "voice"
        attachment.mediaRef = "ref-1"
        attachment.durationMs = 4200
        attachment.waveform = [1, 2, 3, 250]

        var forward = ForwardOriginBody()
        forward.chatID = "10"
        forward.messageID = "11"
        forward.senderID = "12"

        var message = NewMessageBody()
        message.messageID = "900"
        message.chatID = "10"
        message.senderID = "7"
        message.chatSeq = 128
        message.text = "привет 👋"
        message.timestamp = 1_700_000_000_000
        message.attachment = attachment
        message.forward = forward
        message.expiresAt = 1_700_000_060_000
        message.edited = true

        let decoded = try NewMessageBody.protoDecoded(from: message.protoEncoded())
        XCTAssertEqual(decoded, message)
        XCTAssertEqual(decoded.attachment?.waveform, [1, 2, 3, 250])
        XCTAssertEqual(decoded.forward?.senderID, "12")
    }

    /// `repeated int32` is packed by default in proto3, but an older writer may
    /// send it unpacked. Both must decode.
    func testRepeatedInt32AcceptsPackedAndUnpacked() throws {
        var packed = ProtoWriter()
        packed.string(2, "ref")
        packed.packedInt32(7, [5, 6, 7])
        XCTAssertEqual(try AttachmentBody.protoDecoded(from: packed.data).waveform, [5, 6, 7])

        // Hand-built unpacked form: field 7, wire type 0, three times.
        var unpacked = ProtoWriter()
        unpacked.string(2, "ref")
        unpacked.int32(7, 5)
        unpacked.int32(7, 6)
        unpacked.int32(7, 7)
        XCTAssertEqual(try AttachmentBody.protoDecoded(from: unpacked.data).waveform, [5, 6, 7])
    }

    /// `map<string, int32>` is sugar for a repeated {1: key, 2: value} message.
    func testReactionCountsMapRoundTrip() throws {
        var update = ReactUpdateBody()
        update.chatID = "10"
        update.messageID = "900"
        update.emoji = "🔥"
        update.added = true
        update.counts = ["🔥": 3, "👍": 1]

        let decoded = try ReactUpdateBody.protoDecoded(from: update.protoEncoded())
        XCTAssertEqual(decoded.counts, ["🔥": 3, "👍": 1])
    }

    func testMapEncodingIsDeterministic() {
        var update = ReactUpdateBody()
        update.counts = ["b": 2, "a": 1, "c": 3]
        XCTAssertEqual(update.protoEncoded(), update.protoEncoded())
    }

    func testErrorBodyMapsUnknownCodeWithoutLosingTheMessage() throws {
        var writer = ProtoWriter()
        writer.uint32(1, 7777)  // a code this build has never heard of
        writer.string(2, "brand new failure")
        writer.int32(3, 1500)

        let decoded = try ErrorBody.protoDecoded(from: writer.data)
        XCTAssertEqual(decoded.code, .unknown)
        XCTAssertEqual(decoded.message, "brand new failure")
        XCTAssertEqual(decoded.asProtocolError.retryAfterMs, 1500)
    }

    func testEmptyBodyDecodesToDefaults() throws {
        let decoded = try SendAckBody.protoDecoded(from: Data())
        XCTAssertEqual(decoded.messageID, "")
        XCTAssertFalse(decoded.duplicate)
    }
}

/// The error-code *ranges* are the contract — a client must be able to react to
/// a code it has never seen by its class alone.
final class ErrorCodeTests: XCTestCase {

    func testAuthFailuresAreThe2xxxRange() {
        XCTAssertTrue(ErrorCode.unauthenticated.isAuthFailure)
        XCTAssertTrue(ErrorCode.badToken.isAuthFailure)
        XCTAssertTrue(ErrorCode.sessionRevoked.isAuthFailure)
        XCTAssertFalse(ErrorCode.forbidden.isAuthFailure)
    }

    /// An expired resume buffer is recoverable by a full re-AUTH, so it must not
    /// be treated as a dead session — otherwise a routine reconnect would log
    /// the user out.
    func testResumeExpiredIsNotAnAuthFailure() {
        XCTAssertFalse(ErrorCode.resumeExpired.isAuthFailure)
    }

    func testRetryClassification() {
        XCTAssertTrue(ErrorCode.rateLimited.isRetryable)
        XCTAssertTrue(ErrorCode.unavailable.isRetryable)
        XCTAssertFalse(ErrorCode.forbidden.isRetryable)
        XCTAssertFalse(ErrorCode.notFound.isRetryable)
    }
}
