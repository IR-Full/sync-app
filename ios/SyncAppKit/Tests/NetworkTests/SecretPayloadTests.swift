import XCTest
@testable import SyncAppNetwork

/// The two encodings of a secret-chat payload.
///
/// Worth asserting byte-for-byte rather than by round trip, because the failure mode is
/// cross-platform and silent: a client that encodes and decodes wrongly in the same way
/// talks to itself perfectly and to nothing else. The specific trap here is that the two
/// halves of the legacy form are encoded DIFFERENTLY — the header is text, the ciphertext
/// is base64 — which has already been misread once as "both are base64".
final class SecretPayloadTests: XCTestCase {

    private let header = Data(#"{"rh":"AQID"}"#.utf8)
    private let cipher = Data([0xDE, 0xAD, 0xBE, 0xEF])

    func testBinaryFormFillsOnlyTheBytesFields() {
        var body = SecretMsgBody()
        body.setPayload(header, cipher, binary: true)

        XCTAssertEqual(body.ratchetHeaderBin, header)
        XCTAssertEqual(body.ciphertextBin, cipher)
        // Only ONE form is populated. Filling both would double the payload, which is
        // the opposite of why the binary fields exist.
        XCTAssertTrue(body.ratchetHeader.isEmpty)
        XCTAssertTrue(body.ciphertext.isEmpty)
    }

    func testLegacyFormEncodesTheHeaderAsTextAndTheCiphertextAsBase64() {
        var body = SecretMsgBody()
        body.setPayload(header, cipher, binary: false)

        // The asymmetry, stated as an assertion: the header is a JSON object the
        // receiving client PARSES, so its bytes are the UTF-8 of that text; the
        // ciphertext is opaque, so it is base64.
        XCTAssertEqual(body.ratchetHeader, #"{"rh":"AQID"}"#)
        XCTAssertEqual(body.ciphertext, "3q2+7w==")
        XCTAssertTrue(body.ratchetHeaderBin.isEmpty)
        XCTAssertTrue(body.ciphertextBin.isEmpty)
    }

    func testBothFormsReadBackIdentically() {
        var binary = SecretMsgBody()
        binary.setPayload(header, cipher, binary: true)
        var legacy = SecretMsgBody()
        legacy.setPayload(header, cipher, binary: false)

        // The point of the whole exercise: a peer on either encoding hands the crypto
        // layer the same bytes, so nothing above this has to know which arrived.
        let fromBinary = binary.payload()
        let fromLegacy = legacy.payload()
        XCTAssertEqual(fromBinary?.header, header)
        XCTAssertEqual(fromBinary?.ciphertext, cipher)
        XCTAssertEqual(fromLegacy?.header, header)
        XCTAssertEqual(fromLegacy?.ciphertext, cipher)
    }

    func testBinaryWinsWhenAPeerSendsBoth() {
        var body = SecretMsgBody()
        body.ratchetHeaderBin = header
        body.ciphertextBin = cipher
        body.ratchetHeader = "stale"
        body.ciphertext = "c3RhbGU="

        // A sender that filled both is ambiguous, and the binary fields are the ones
        // that cannot have been mangled by a trip through base64. The server applies the
        // same rule, so the two ends cannot disagree about which payload a frame carries.
        XCTAssertEqual(body.payload()?.header, header)
        XCTAssertEqual(body.payload()?.ciphertext, cipher)
    }

    func testAnEmptyPayloadIsNil() {
        XCTAssertNil(SecretMsgBody().payload())
    }

    func testNonBase64CiphertextIsRejectedRatherThanGuessed() {
        var body = SecretMsgBody()
        body.ratchetHeader = #"{"rh":"AQID"}"#
        body.ciphertext = "!!! not base64 !!!"
        // nil, not empty Data. Handing the ratchet an empty ciphertext would fail the
        // AEAD and be logged as "did not decrypt", which points at the crypto instead of
        // at the malformed frame that actually caused it.
        XCTAssertNil(body.payload())
    }

    func testTheHeaderIsNotDecodedAsBase64() {
        // The regression this guards. `{"rh":"AQID"}` happens to contain only base64
        // alphabet characters plus punctuation; a decoder that tried would either throw
        // or — worse — succeed on some inputs and produce garbage.
        var body = SecretMsgBody()
        body.setPayload(header, cipher, binary: false)
        XCTAssertEqual(body.payload()?.header, header)
        XCTAssertEqual(String(decoding: body.payload()?.header ?? Data(), as: UTF8.self),
                       #"{"rh":"AQID"}"#)
    }

    func testProtoRoundTripKeepsTheBytesFields() throws {
        var body = SecretMsgBody()
        body.toUserID = "u1"
        body.toDeviceID = "d1"
        body.fromUserID = "u2"
        body.fromDeviceID = "d2"
        body.queueID = "q1"
        body.setPayload(header, cipher, binary: true)

        let decoded = try SecretMsgBody.protoDecoded(from: body.protoEncoded())
        XCTAssertEqual(decoded, body)
        XCTAssertEqual(decoded.payload()?.ciphertext, cipher)
    }

    func testSecretAckDistinguishesTheThreeOutcomes() {
        var delivered = SecretAckBody()
        delivered.devices = 2
        XCTAssertTrue(delivered.delivered)
        XCTAssertFalse(delivered.pending)
        XCTAssertFalse(delivered.undeliverable)

        var pending = SecretAckBody()
        pending.queued = true
        XCTAssertFalse(pending.delivered)
        XCTAssertTrue(pending.pending)
        XCTAssertFalse(pending.undeliverable)

        // The case that used to be indistinguishable from success, because the relay
        // answered nothing at all: the peer has published no device keys, so there is
        // nowhere to deliver AND nothing to queue against. Waiting will not help.
        let nowhere = SecretAckBody()
        XCTAssertFalse(nowhere.delivered)
        XCTAssertFalse(nowhere.pending)
        XCTAssertTrue(nowhere.undeliverable)
    }
}
