import XCTest
@testable import SyncAppCrypto
@testable import SyncAppPersistence

/// The JSON envelope that carries the X3DH bootstrap inside the ratchet-header slot.
///
/// Every assertion here is about EXACT bytes, and that is not fussiness: the envelope is
/// authenticated as the AEAD's additional data, so a different spelling of the same
/// meaning changes the tag. The Go and TypeScript implementations already emit this
/// shape, so it cannot be improved unilaterally — a "cleaner" encoding on iOS makes
/// every message from iOS undecryptable everywhere else.
final class SecretEnvelopeTests: XCTestCase {

    func testFirstMessageEnvelopeMatchesTheOtherImplementations() {
        let envelope = SecretEnvelope(
            identityKey: "SUsx",
            ephemeralKey: "RUsx",
            ratchetHeader: "Ukgx"
        )
        XCTAssertEqual(
            String(decoding: envelope.encoded(), as: UTF8.self),
            #"{"ik":"SUsx","ek":"RUsx","rh":"Ukgx"}"#
        )
    }

    func testLaterMessagesCarryOnlyTheHeader() {
        let envelope = SecretEnvelope(identityKey: nil, ephemeralKey: nil, ratchetHeader: "Ukgx")
        XCTAssertEqual(String(decoding: envelope.encoded(), as: UTF8.self), #"{"rh":"Ukgx"}"#)
    }

    func testRoundTrip() {
        let envelope = SecretEnvelope(
            identityKey: "SUsx",
            ephemeralKey: "RUsx",
            ratchetHeader: "Ukgx"
        )
        XCTAssertEqual(SecretEnvelope.decode(envelope.encoded()), envelope)
    }

    func testDecodeToleratesFieldOrderAndWhitespace() {
        // Written by another implementation, so it is read with a real JSON parser rather
        // than matched. Only the WRITE side is hand-built, because only the write side
        // has to produce specific bytes.
        let json = Data(#"{ "rh" : "Ukgx", "ek": "RUsx", "ik":"SUsx" }"#.utf8)
        let decoded = SecretEnvelope.decode(json)
        XCTAssertEqual(decoded?.ratchetHeader, "Ukgx")
        XCTAssertEqual(decoded?.identityKey, "SUsx")
        XCTAssertEqual(decoded?.ephemeralKey, "RUsx")
    }

    func testBootstrapNeedsBothHalves() {
        let both = SecretEnvelope(identityKey: "SUsx", ephemeralKey: "RUsx", ratchetHeader: "Ukgx")
        XCTAssertNotNil(both.bootstrap)

        // Half an envelope is unusable, and this is the bug that made the retry path
        // worth rewriting: a send that failed after the ratchet advanced used to resend
        // `ik` with the ephemeral already discarded, producing exactly this.
        let identityOnly = SecretEnvelope(identityKey: "SUsx", ephemeralKey: nil, ratchetHeader: "Ukgx")
        XCTAssertNil(identityOnly.bootstrap)
        let ephemeralOnly = SecretEnvelope(identityKey: nil, ephemeralKey: "RUsx", ratchetHeader: "Ukgx")
        XCTAssertNil(ephemeralOnly.bootstrap)
    }

    func testMalformedEnvelopesReturnNil() {
        // Relay-supplied data: one bad frame must not take the connection down, so every
        // rejection is a nil rather than a throw.
        XCTAssertNil(SecretEnvelope.decode(Data()))
        XCTAssertNil(SecretEnvelope.decode(Data("not json".utf8)))
        XCTAssertNil(SecretEnvelope.decode(Data(#"{"ik":"SUsx"}"#.utf8)), "no rh")
        XCTAssertNil(SecretEnvelope.decode(Data(#"{"rh":""}"#.utf8)), "empty rh")
        XCTAssertNil(SecretEnvelope.decode(Data(#"[1,2,3]"#.utf8)), "not an object")
    }

    func testHeaderIsBase64OfTheMarshalledRatchetHeader() {
        // Two layers: the ratchet header is itself JSON, base64'd into `rh`, inside this
        // JSON. Wasteful and load-bearing — the bytes are authenticated.
        let header = RatchetHeader(dh: Data([0x01, 0x02, 0x03]), pn: 7, n: 42)
        let envelope = SecretEnvelope(
            identityKey: nil,
            ephemeralKey: nil,
            ratchetHeader: B64.encode(RatchetHeaderCodec.marshal(header))
        )
        XCTAssertEqual(envelope.header, header)

        let reparsed = SecretEnvelope.decode(envelope.encoded())
        XCTAssertEqual(reparsed?.header, header)
    }

    func testAnUnparseableRatchetHeaderIsNil() {
        let envelope = SecretEnvelope(identityKey: nil, ephemeralKey: nil, ratchetHeader: "!!!")
        XCTAssertNil(envelope.header)
    }
}
