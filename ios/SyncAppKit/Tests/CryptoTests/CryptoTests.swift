import CryptoKit
import XCTest
@testable import SyncAppCrypto

/// The first tests this module has ever had, because it is the first time it has been
/// compiled: `Sources/Crypto` was not declared in `Package.swift`, and SPM ignores
/// sources outside every target path without a word. X3DH, the ratchet, safety numbers
/// and trust pinning were all written, reviewed and then never built.
///
/// What is asserted here is therefore deliberately basic — does it work at all — plus
/// the handful of byte-level details that have to match the Go, TypeScript and Kotlin
/// ports exactly. Those are the ones with no local symptom: a header serialised one
/// character differently still round-trips against itself and fails only against
/// another platform, weeks later, as "decryption failed".
final class KeyPrimitiveTests: XCTestCase {

    func testDiffieHellmanAgrees() {
        let a = Crypto.generateKeyPair()
        let b = Crypto.generateKeyPair()
        let ab = Crypto.diffieHellman(privateKey: a.privateKey, publicKey: b.publicKey)
        let ba = Crypto.diffieHellman(privateKey: b.privateKey, publicKey: a.publicKey)
        XCTAssertNotNil(ab)
        XCTAssertEqual(ab, ba)
        XCTAssertEqual(ab?.count, Crypto.keyLength)
    }

    func testDiffieHellmanRejectsMalformedKeys() {
        let a = Crypto.generateKeyPair()
        XCTAssertNil(Crypto.diffieHellman(privateKey: a.privateKey, publicKey: Data([1, 2, 3])))
        XCTAssertNil(Crypto.diffieHellman(privateKey: Data(), publicKey: a.publicKey))
    }

    func testPreKeySignatureRoundTrip() {
        let signing = Crypto.generateSigningKeyPair()
        let prekey = Crypto.generateKeyPair()
        guard let sig = Crypto.signPreKey(
            signingPrivateKey: signing.privateKey,
            signedPreKeyPublic: prekey.publicKey
        ) else { return XCTFail("signing failed") }

        XCTAssertEqual(sig.count, Crypto.signatureLength)
        XCTAssertTrue(Crypto.verifyPreKey(
            signingPublicKey: signing.publicKey,
            signedPreKeyPublic: prekey.publicKey,
            signature: sig
        ))
    }

    func testAnEmptySignatureNeverVerifies() {
        // The MITM defence rests on this. A hostile key directory does not need to
        // forge a signature if an absent one is treated as "nothing to check" — it
        // just omits the field and serves a prekey it holds the private half of.
        let signing = Crypto.generateSigningKeyPair()
        let prekey = Crypto.generateKeyPair()
        XCTAssertFalse(Crypto.verifyPreKey(
            signingPublicKey: signing.publicKey,
            signedPreKeyPublic: prekey.publicKey,
            signature: Data()
        ))
    }

    func testASignatureFromAnotherIdentityDoesNotVerify() {
        let real = Crypto.generateSigningKeyPair()
        let attacker = Crypto.generateSigningKeyPair()
        let prekey = Crypto.generateKeyPair()
        guard let sig = Crypto.signPreKey(
            signingPrivateKey: attacker.privateKey,
            signedPreKeyPublic: prekey.publicKey
        ) else { return XCTFail("signing failed") }

        XCTAssertFalse(Crypto.verifyPreKey(
            signingPublicKey: real.publicKey,
            signedPreKeyPublic: prekey.publicKey,
            signature: sig
        ))
    }

    func testHKDFMatchesRFC5869VectorOne() {
        // RFC 5869 A.1. A cross-platform fixed vector rather than a round trip:
        // the ratchet's every key comes out of this function, so an HKDF that is
        // self-consistently wrong produces a session that talks only to itself.
        let ikm = Data(repeating: 0x0b, count: 22)
        let salt = Data([0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c])
        let info = Data([0xf0, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9])
        let okm = Crypto.hkdf(ikm: ikm, salt: salt, info: info, length: 42)
        XCTAssertEqual(
            okm.map { String(format: "%02x", $0) }.joined(),
            "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865"
        )
    }

    func testConstantTimeEqualsIsLengthSafe() {
        XCTAssertTrue(Crypto.constantTimeEquals(Data([1, 2, 3]), Data([1, 2, 3])))
        XCTAssertFalse(Crypto.constantTimeEquals(Data([1, 2, 3]), Data([1, 2, 4])))
        XCTAssertFalse(Crypto.constantTimeEquals(Data([1, 2, 3]), Data([1, 2])))
        XCTAssertTrue(Crypto.constantTimeEquals(Data(), Data()))
    }

    func testBase64IsStandardNotURLSafe() {
        // The gateway decodes prekeys with Go's StdEncoding. A url-safe alphabet
        // produces keys it rejects as malformed, and the rejection arrives as a
        // generic error long after the publish that caused it.
        let bytes = Data([0xFB, 0xFF, 0xBE])
        let encoded = B64.encode(bytes)
        XCTAssertEqual(encoded, "+/++")
        XCTAssertEqual(B64.decode(encoded), bytes)
    }
}

// MARK: - X3DH

final class X3DHTests: XCTestCase {

    /// Both halves of a handshake, wired the way the app has to wire them.
    private struct Party {
        let identity: SecretKeyPair
        let signing: SigningKeyPair
        let signedPreKey: SecretKeyPair
        let oneTimePreKey: SecretKeyPair
        let signature: Data

        init() {
            identity = Crypto.generateKeyPair()
            signing = Crypto.generateSigningKeyPair()
            signedPreKey = Crypto.generateKeyPair()
            oneTimePreKey = Crypto.generateKeyPair()
            signature = Crypto.signPreKey(
                signingPrivateKey: signing.privateKey,
                signedPreKeyPublic: signedPreKey.publicKey
            ) ?? Data()
        }

        func bundle(withOneTime: Bool) -> PreKeyBundle {
            PreKeyBundle(
                identityKey: identity.publicKey,
                signingKey: signing.publicKey,
                signedPreKey: signedPreKey.publicKey,
                signedPreKeySignature: signature,
                oneTimePreKey: withOneTime ? oneTimePreKey.publicKey : Data()
            )
        }
    }

    func testBothSidesDeriveTheSameSecretWithAOneTimePreKey() throws {
        let alice = Party()
        let bob = Party()
        let ephemeral = Crypto.generateKeyPair()

        let (secret, ephemeralPublic) = try X3DH.initiator(
            keys: InitiatorKeys(identity: alice.identity, ephemeral: ephemeral),
            bundle: bob.bundle(withOneTime: true)
        )
        XCTAssertEqual(ephemeralPublic, ephemeral.publicKey)

        let theirs = try X3DH.responder(
            keys: ResponderKeys(
                identity: bob.identity,
                signedPreKey: bob.signedPreKey,
                oneTimePreKey: bob.oneTimePreKey
            ),
            initiatorIdentity: alice.identity.publicKey,
            initiatorEphemeral: ephemeralPublic,
            usedOneTime: true
        )
        XCTAssertEqual(secret, theirs)
    }

    func testBothSidesAgreeWhenThePreKeyBatchHasRunDry() throws {
        // An exhausted batch is the normal state of a popular account, not an error:
        // the handshake falls back to the signed prekey. What must not happen is the
        // two sides disagreeing about whether DH4 was included, which produces two
        // valid-looking secrets and a session that never decrypts anything.
        let alice = Party()
        let bob = Party()
        let ephemeral = Crypto.generateKeyPair()

        let (secret, ephemeralPublic) = try X3DH.initiator(
            keys: InitiatorKeys(identity: alice.identity, ephemeral: ephemeral),
            bundle: bob.bundle(withOneTime: false)
        )
        let theirs = try X3DH.responder(
            keys: ResponderKeys(
                identity: bob.identity,
                signedPreKey: bob.signedPreKey,
                oneTimePreKey: nil
            ),
            initiatorIdentity: alice.identity.publicKey,
            initiatorEphemeral: ephemeralPublic,
            usedOneTime: false
        )
        XCTAssertEqual(secret, theirs)
    }

    func testASubstitutedPreKeyIsRejected() {
        let bob = Party()
        let attacker = Party()
        let alice = Party()

        // The shape of a hostile key directory: Bob's advertised identity, the
        // attacker's prekey. Nothing else in the bundle needs changing.
        let forged = PreKeyBundle(
            identityKey: bob.identity.publicKey,
            signingKey: bob.signing.publicKey,
            signedPreKey: attacker.signedPreKey.publicKey,
            signedPreKeySignature: bob.signature,
            oneTimePreKey: Data()
        )

        XCTAssertThrowsError(
            try X3DH.initiator(
                keys: InitiatorKeys(identity: alice.identity, ephemeral: Crypto.generateKeyPair()),
                bundle: forged
            )
        ) { error in
            XCTAssertEqual(error as? X3DHError, .badPreKeySignature)
        }
    }

    func testAnUnsignedBundleIsRejected() {
        let bob = Party()
        let alice = Party()
        let unsigned = PreKeyBundle(
            identityKey: bob.identity.publicKey,
            signingKey: bob.signing.publicKey,
            signedPreKey: bob.signedPreKey.publicKey,
            signedPreKeySignature: Data(),
            oneTimePreKey: Data()
        )
        XCTAssertThrowsError(
            try X3DH.initiator(
                keys: InitiatorKeys(identity: alice.identity, ephemeral: Crypto.generateKeyPair()),
                bundle: unsigned
            )
        ) { error in
            XCTAssertEqual(error as? X3DHError, .badPreKeySignature)
        }
    }

    func testResponderRefusesAOneTimePreKeyItDoesNotHold() {
        let bob = Party()
        XCTAssertThrowsError(
            try X3DH.responder(
                keys: ResponderKeys(
                    identity: bob.identity,
                    signedPreKey: bob.signedPreKey,
                    oneTimePreKey: nil
                ),
                initiatorIdentity: Crypto.generateKeyPair().publicKey,
                initiatorEphemeral: Crypto.generateKeyPair().publicKey,
                usedOneTime: true
            )
        ) { error in
            XCTAssertEqual(error as? X3DHError, .missingOneTimePreKey)
        }
    }
}

// MARK: - Double Ratchet

final class RatchetTests: XCTestCase {

    /// A pair of sessions past the handshake, which is what every test below needs.
    private func pair() throws -> (alice: RatchetSession, bob: RatchetSession, bobSPK: SecretKeyPair) {
        let bobSPK = Crypto.generateKeyPair()
        let secret = Data(repeating: 0x2a, count: 32)
        guard let alice = RatchetSession.initiator(
            sharedSecret: secret,
            theirSignedPreKey: bobSPK.publicKey
        ) else {
            throw XCTSkip("initiator session could not be built")
        }
        let bob = RatchetSession.responder(sharedSecret: secret, signedPreKey: bobSPK)
        return (alice, bob, bobSPK)
    }

    func testOneMessageEachWay() throws {
        let (alice, bob, _) = try pair()

        let hello = Data("hello".utf8)
        let (h1, c1) = try alice.encrypt(hello)
        XCTAssertEqual(try bob.decrypt(header: h1, ciphertext: c1), hello)

        // Bob had no sending chain until that arrived — the DH ratchet step on
        // receipt is what gives him one.
        let reply = Data("hi back".utf8)
        let (h2, c2) = try bob.encrypt(reply)
        XCTAssertEqual(try alice.decrypt(header: h2, ciphertext: c2), reply)
    }

    func testResponderCannotSendFirst() throws {
        let bobSPK = Crypto.generateKeyPair()
        let bob = RatchetSession.responder(
            sharedSecret: Data(repeating: 0x2a, count: 32),
            signedPreKey: bobSPK
        )
        XCTAssertThrowsError(try bob.encrypt(Data("too early".utf8))) { error in
            XCTAssertEqual(error as? RatchetError, .noSendingChain)
        }
    }

    func testOutOfOrderDeliveryStillDecrypts() throws {
        let (alice, bob, _) = try pair()

        var frames: [(RatchetHeader, Data)] = []
        for i in 0..<5 {
            frames.append(try alice.encrypt(Data("m\(i)".utf8)))
        }

        // Arrive backwards. The relay does not promise ordering across a reconnect,
        // and the offline queue replays in its own order.
        for i in (0..<5).reversed() {
            XCTAssertEqual(
                try bob.decrypt(header: frames[i].0, ciphertext: frames[i].1),
                Data("m\(i)".utf8)
            )
        }
    }

    func testAMessageKeyIsSingleUse() throws {
        let (alice, bob, _) = try pair()
        let (h, c) = try alice.encrypt(Data("once".utf8))
        XCTAssertEqual(try bob.decrypt(header: h, ciphertext: c), Data("once".utf8))
        // A replayed frame must not open a second time.
        XCTAssertThrowsError(try bob.decrypt(header: h, ciphertext: c))
    }

    func testATamperedHeaderFailsAndLeavesTheSessionUsable() throws {
        let (alice, bob, _) = try pair()

        let (good, cipher) = try alice.encrypt(Data("payload".utf8))
        // The header is authenticated as the AEAD's additional data, so changing a
        // counter has to fail — and, more importantly, must not ratchet the session
        // on the way. The relay lets any account address any device, so a forged
        // frame is free to send.
        let forged = RatchetHeader(dh: good.dh, pn: good.pn, n: good.n + 1)
        XCTAssertThrowsError(try bob.decrypt(header: forged, ciphertext: cipher))
        // The genuine frame still opens afterwards. If the failed attempt had moved
        // the receiving chain, this would be the assertion that caught it.
        XCTAssertEqual(try bob.decrypt(header: good, ciphertext: cipher), Data("payload".utf8))
    }

    func testAForgedRatchetKeyDoesNotRewriteTheSession() throws {
        let (alice, bob, _) = try pair()

        let stranger = Crypto.generateKeyPair()
        let forged = RatchetHeader(dh: stranger.publicKey, pn: 0, n: 0)
        XCTAssertThrowsError(try bob.decrypt(header: forged, ciphertext: Data(repeating: 0, count: 32)))

        let hello = Data("still works".utf8)
        let (h, c) = try alice.encrypt(hello)
        XCTAssertEqual(try bob.decrypt(header: h, ciphertext: c), hello)
    }

    func testAHeaderThatRoundTrippedThroughTheWireStillOpens() throws {
        let (alice, bob, _) = try pair()
        let (header, cipher) = try alice.encrypt(Data("hello".utf8))

        let parsed = try XCTUnwrap(RatchetHeaderCodec.unmarshal(RatchetHeaderCodec.marshal(header)))
        XCTAssertEqual(try bob.decrypt(header: parsed, ciphertext: cipher), Data("hello".utf8))
    }

    func testAHeaderEditedInFlightIsRejected() throws {
        // Carrying the bytes must not weaken the check it feeds: the counters and
        // the ratchet key are covered because they are inside the verified bytes.
        let (alice, bob, _) = try pair()
        let (header, cipher) = try alice.encrypt(Data("hello".utf8))

        let onWire = String(decoding: RatchetHeaderCodec.marshal(header), as: UTF8.self)
        let forged = onWire.replacingOccurrences(of: #""n":0"#, with: #""n":7"#)
        XCTAssertNotEqual(forged, onWire, "the test did not actually change the header")

        let parsed = try XCTUnwrap(RatchetHeaderCodec.unmarshal(Data(forged.utf8)))
        XCTAssertThrowsError(try bob.decrypt(header: parsed, ciphertext: cipher))
    }

    func testSessionSurvivesSerialisation() throws {
        let (alice, bob, _) = try pair()

        let (h1, c1) = try alice.encrypt(Data("before".utf8))
        XCTAssertEqual(try bob.decrypt(header: h1, ciphertext: c1), Data("before".utf8))

        // What persistence actually does: write the state, drop the object, read it
        // back. A session that cannot survive this loses every conversation on app
        // restart, which on a phone is roughly every few minutes.
        guard let restored = RatchetSession.deserialize(bob.serialize()) else {
            return XCTFail("a session it just serialised could not be read back")
        }

        let (h2, c2) = try alice.encrypt(Data("after".utf8))
        XCTAssertEqual(try restored.decrypt(header: h2, ciphertext: c2), Data("after".utf8))
    }

    func testSerialisedStateIsJSONCodable() throws {
        // `SerializedSession` is what lands in SQLite. Base64 strings throughout
        // rather than `Data`, because `JSONEncoder` renders `Data` differently across
        // its own strategies and the stored form has to be readable by whatever reads
        // it next.
        let (alice, _, _) = try pair()
        let state = alice.serialize()
        let encoded = try JSONEncoder().encode(state)
        let decoded = try JSONDecoder().decode(SerializedSession.self, from: encoded)
        XCTAssertEqual(state, decoded)
    }
}

// MARK: - Header serialisation

final class RatchetHeaderCodecTests: XCTestCase {

    func testHeaderJSONMatchesGoFieldOrderAndSpacing() {
        // The header IS the AEAD's additional data, so these bytes are authenticated.
        // Reordering the keys or adding a space does not change the meaning and does
        // change the tag — which is why this is hand-built rather than handed to
        // `JSONEncoder`, and why the exact string is asserted.
        let header = RatchetHeader(dh: Data([0x01, 0x02, 0x03]), pn: 7, n: 42)
        XCTAssertEqual(
            String(decoding: RatchetHeaderCodec.marshal(header), as: UTF8.self),
            #"{"dh":"AQID","pn":7,"n":42}"#
        )
    }

    func testRoundTrip() {
        let header = RatchetHeader(dh: Data([0xFF, 0x00, 0xAB]), pn: 3, n: 0)
        let bytes = RatchetHeaderCodec.marshal(header)
        XCTAssertEqual(RatchetHeaderCodec.unmarshal(bytes), header)
    }

    func testOmittedCountersReadAsZero() {
        // Go omits a zero-valued field under `omitempty`, so a header from the server
        // side can legitimately arrive without `pn` or `n`.
        let parsed = RatchetHeaderCodec.unmarshal(Data(#"{"dh":"AQID"}"#.utf8))
        XCTAssertEqual(parsed, RatchetHeader(dh: Data([1, 2, 3]), pn: 0, n: 0))
    }

    func testNegativeCountersAreRejected() {
        // Not pedantry: the skip loop converts these to unsigned, so a -1 becomes an
        // enormous gap and the session spends `maxSkip` HMAC derivations on a frame
        // that was never going to decrypt.
        XCTAssertNil(RatchetHeaderCodec.unmarshal(Data(#"{"dh":"AQID","pn":-1,"n":0}"#.utf8)))
        XCTAssertNil(RatchetHeaderCodec.unmarshal(Data(#"{"dh":"AQID","pn":0,"n":-5}"#.utf8)))
    }

    // MARK: - The header is authenticated as the bytes it travelled as
    //
    // Without this, interop rests on four independent implementations emitting
    // byte-identical canonical JSON forever, with nothing enforcing it and a
    // `decryptionFailed` — which reads exactly like a forgery — as the only
    // symptom when one of them drifts. Mirrors the Go tests in
    // `server/pkg/e2e/e2e_test.go`, the TypeScript ones in
    // `client/src/shared/lib/e2e/ratchet.test.ts` and the Kotlin ones in
    // `android/.../crypto/CryptoInteropTest.kt`.

    func testAPeersNonCanonicalEncodingIsKeptVerbatim() throws {
        let foreign = Data(#"{ "n": 3, "pn": 1, "dh": "AQID" }"#.utf8)

        let parsed = try XCTUnwrap(RatchetHeaderCodec.unmarshal(foreign))
        XCTAssertEqual(parsed.n, 3)
        XCTAssertEqual(parsed.pn, 1)
        XCTAssertEqual(RatchetHeaderCodec.marshal(parsed), foreign)
    }

    func testAHeaderRebuiltFromValuesIsEncodedCanonically() throws {
        // Rebuilding through the public initialiser drops the bytes, which is what
        // stops the parsed counters and the authenticated bytes from ever
        // disagreeing — the ratchet must never advance on a counter the AEAD did
        // not verify.
        let parsed = try XCTUnwrap(
            RatchetHeaderCodec.unmarshal(Data(#"{ "n": 3, "pn": 1, "dh": "AQID" }"#.utf8))
        )
        let rebuilt = RatchetHeader(dh: parsed.dh, pn: 99, n: parsed.n)

        XCTAssertEqual(
            String(decoding: RatchetHeaderCodec.marshal(rebuilt), as: UTF8.self),
            #"{"dh":"AQID","pn":99,"n":3}"#
        )
    }

    func testMalformedHeadersReturnNil() {
        XCTAssertNil(RatchetHeaderCodec.unmarshal(Data()))
        XCTAssertNil(RatchetHeaderCodec.unmarshal(Data("not json".utf8)))
        XCTAssertNil(RatchetHeaderCodec.unmarshal(Data(#"{"pn":1,"n":2}"#.utf8)), "no dh")
        XCTAssertNil(RatchetHeaderCodec.unmarshal(Data(#"{"dh":"!!!not base64!!!"}"#.utf8)))
    }
}

// MARK: - Safety numbers

final class SafetyNumberTests: XCTestCase {

    private func identity(_ id: String) -> Safety.Identity {
        Safety.Identity(
            stableID: id,
            identityKey: Crypto.generateKeyPair().publicKey,
            signingKey: Crypto.generateSigningKeyPair().publicKey
        )
    }

    func testBothPartiesComputeTheSameNumber() {
        let a = identity("alice")
        let b = identity("bob")
        // The two sides disagree about which of them is "local", so a number that
        // depended on argument order would differ on the two phones — and the users
        // comparing them would conclude they are being intercepted.
        XCTAssertEqual(
            Safety.number(local: a, remote: b),
            Safety.number(local: b, remote: a)
        )
    }

    func testTheNumberIsSixtyDigitsInSixGroups() {
        let number = Safety.number(local: identity("a"), remote: identity("b"))
        let groups = number.split(separator: " ").map(String.init)
        XCTAssertEqual(groups.count, 12, "two fingerprints of six groups each")
        for group in groups {
            XCTAssertEqual(group.count, 5)
            XCTAssertTrue(group.allSatisfy(\.isNumber), "\(group) should be digits only")
        }
    }

    func testAChangedIdentityKeyChangesTheNumber() {
        // This is the entire signal the feature carries. If it did not hold, the
        // number would be decoration.
        let a = identity("alice")
        let b = identity("bob")
        let bRekeyed = Safety.Identity(
            stableID: b.stableID,
            identityKey: Crypto.generateKeyPair().publicKey,
            signingKey: b.signingKey
        )
        XCTAssertNotEqual(
            Safety.number(local: a, remote: b),
            Safety.number(local: a, remote: bRekeyed)
        )
    }

    func testAChangedSigningKeyAlsoChangesTheNumber() {
        // Binding both halves of the identity means a MITM has to forge the whole
        // thing rather than the half that happens to be checked.
        let a = identity("alice")
        let b = identity("bob")
        let bResigned = Safety.Identity(
            stableID: b.stableID,
            identityKey: b.identityKey,
            signingKey: Crypto.generateSigningKeyPair().publicKey
        )
        XCTAssertNotEqual(
            Safety.number(local: a, remote: b),
            Safety.number(local: a, remote: bResigned)
        )
    }
}

// MARK: - Trust on first use

final class TrustStoreTests: XCTestCase {

    private let identityKey = B64.encode(Data(repeating: 0x11, count: 32))
    private let signingKey = B64.encode(Data(repeating: 0x22, count: 32))

    func testFirstSightIsFirstUseThenKnown() {
        let store = TrustStore()
        XCTAssertEqual(
            store.verify(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey),
            .firstUse
        )
        store.accept(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey)
        XCTAssertEqual(
            store.verify(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey),
            .known
        )
    }

    func testAChangedKeyReportsWhatWasPinned() {
        let store = TrustStore(now: { 1_700_000_000_000 })
        store.accept(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey)
        let rotated = B64.encode(Data(repeating: 0x33, count: 32))

        // A key that changed under a pinned device is either a reinstall or an
        // interception, and nothing here can tell which. The verdict CARRIES the old
        // pin because the screen that asks the user has to show both — a bare "changed"
        // would leave the caller re-reading the store to say anything useful.
        switch store.verify(userID: "u1", deviceID: "d1", identityKey: rotated, signingKey: signingKey) {
        case .changed(let pinned):
            XCTAssertEqual(pinned.identityKey, identityKey)
            XCTAssertEqual(pinned.firstSeen, 1_700_000_000_000)
        default:
            XCTFail("a rotated key must not read as known")
        }
    }

    func testAChangedSigningKeyAloneIsAlsoAMismatch() {
        // Both halves are pinned. Checking only the agreement key would let an
        // attacker keep it and swap the signing key, which is the half that vouches
        // for every prekey the device publishes afterwards.
        let store = TrustStore()
        store.accept(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey)
        let resigned = B64.encode(Data(repeating: 0x44, count: 32))
        switch store.verify(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: resigned) {
        case .changed: break
        default: XCTFail("a rotated signing key must not read as known")
        }
    }

    func testForgettingADeviceReturnsItToFirstUse() {
        let store = TrustStore()
        store.accept(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey)
        store.forget(userID: "u1", deviceID: "d1")
        XCTAssertEqual(
            store.verify(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey),
            .firstUse
        )
    }

    func testDevicesArePinnedIndependently() {
        let store = TrustStore()
        store.accept(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey)
        // A second phone is a second identity, not a conflict with the first.
        XCTAssertEqual(
            store.verify(userID: "u1", deviceID: "d2", identityKey: identityKey, signingKey: signingKey),
            .firstUse
        )
    }

    func testPinsRoundTripThroughASnapshot() {
        // `snapshot()` is what the persistence layer writes; a store rebuilt from it
        // has to make the same decisions, or trust resets on every app launch and
        // every device reads as first-use forever.
        let store = TrustStore()
        store.accept(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey)
        let restored = TrustStore(pins: store.snapshot())
        XCTAssertEqual(
            restored.verify(userID: "u1", deviceID: "d1", identityKey: identityKey, signingKey: signingKey),
            .known
        )
    }
}
