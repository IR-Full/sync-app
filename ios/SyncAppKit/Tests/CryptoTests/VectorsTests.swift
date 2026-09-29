import CryptoKit
import XCTest
@testable import SyncAppCrypto

/// Replays `server/testdata/e2e/vectors.json`, produced by the Go implementation,
/// and requires this port to reproduce it byte for byte. With the ratchet keys
/// fixed, encryption is deterministic: matching Go's ciphertext means Go can
/// decrypt ours, and decrypting Go's means we can read Go's. The web and Android
/// clients replay the same file.
final class VectorsTests: XCTestCase {

    private struct VecKeyPair: Decodable {
        let `private`: Data
        let `public`: Data
        var keyPair: SecretKeyPair { SecretKeyPair(privateKey: `private`, publicKey: `public`) }
    }

    private struct VecSigning: Decodable {
        let seed: Data
        let `public`: Data
    }

    private struct VecX3DH: Decodable {
        let aliceIdentity: VecKeyPair
        let aliceEphemeral: VecKeyPair
        let bobIdentity: VecKeyPair
        let bobSigning: VecSigning
        let bobSignedPrekey: VecKeyPair
        let bobOneTimePrekey: VecKeyPair
        let signedPrekeySignature: Data
        let sharedSecret: Data
        let sharedSecretNoOneTime: Data
    }

    private struct VecStep: Decodable {
        let op: String
        let from: String?
        let to: String?
        let id: String
        let plaintext: String?
        let header: String?
        let ciphertext: Data?
    }

    private struct VecConversation: Decodable {
        let aliceRatchetKeys: [VecKeyPair]
        let bobRatchetKeys: [VecKeyPair]
        let steps: [VecStep]
    }

    private struct VecSafety: Decodable {
        let localId: String
        let localIdentityKey: Data
        let localSigningKey: Data
        let remoteId: String
        let remoteIdentityKey: Data
        let remoteSigningKey: Data
        let number: String
    }

    private struct Vectors: Decodable {
        let x3dh: VecX3DH
        let conversation: VecConversation
        let safety: [VecSafety]
    }

    /// Hands out recorded key pairs in order and counts how many were drawn.
    private final class Recorded {
        private let keys: [SecretKeyPair]
        private(set) var drawn = 0
        init(_ keys: [VecKeyPair]) { self.keys = keys.map(\.keyPair) }
        /// Past the end it hands out a fresh key rather than trapping, so the run
        /// reports the mismatch and the `drawn` count instead of crashing.
        func next() -> SecretKeyPair {
            defer { drawn += 1 }
            return drawn < keys.count ? keys[drawn] : Crypto.generateKeyPair()
        }
    }

    /// Walks up from this file, so the test runs from `ios/SyncAppKit` in CI and
    /// from the repository root by hand. A missing file fails the test: a vector
    /// test that quietly does nothing is the drift it exists to catch.
    private func loadVectors() throws -> Vectors {
        var dir = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        while dir.pathComponents.count > 1 {
            let candidate = dir.appendingPathComponent("server/testdata/e2e/vectors.json")
            if FileManager.default.fileExists(atPath: candidate.path) {
                let decoder = JSONDecoder()
                decoder.keyDecodingStrategy = .convertFromSnakeCase
                return try decoder.decode(Vectors.self, from: Data(contentsOf: candidate))
            }
            dir = dir.deletingLastPathComponent()
        }
        XCTFail("server/testdata/e2e/vectors.json not found above \(#filePath)")
        throw CocoaError(.fileNoSuchFile)
    }

    func testEveryPublicKeyDerivesFromItsPrivateKey() throws {
        let v = try loadVectors()
        let x = v.x3dh
        let pairs = [x.aliceIdentity, x.aliceEphemeral, x.bobIdentity, x.bobSignedPrekey, x.bobOneTimePrekey]
            + v.conversation.aliceRatchetKeys + v.conversation.bobRatchetKeys
        for kp in pairs {
            let derived = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: kp.private)
            XCTAssertEqual(derived.publicKey.rawRepresentation, kp.public)
        }
        let signing = try Curve25519.Signing.PrivateKey(rawRepresentation: x.bobSigning.seed)
        XCTAssertEqual(signing.publicKey.rawRepresentation, x.bobSigning.public)
    }

    /// CryptoKit's Ed25519 signatures are randomised, so the bytes cannot match
    /// Go's deterministic ones. What interop needs is that each side accepts the
    /// other's: Go's signature verifies here, and ours verifies under the same key.
    func testPreKeySignaturesVerifyAcrossImplementations() throws {
        let x = try loadVectors().x3dh
        XCTAssertTrue(Crypto.verifyPreKey(
            signingPublicKey: x.bobSigning.public,
            signedPreKeyPublic: x.bobSignedPrekey.public,
            signature: x.signedPrekeySignature
        ))
        let ours = try XCTUnwrap(Crypto.signPreKey(
            signingPrivateKey: x.bobSigning.seed,
            signedPreKeyPublic: x.bobSignedPrekey.public
        ))
        XCTAssertTrue(Crypto.verifyPreKey(
            signingPublicKey: x.bobSigning.public,
            signedPreKeyPublic: x.bobSignedPrekey.public,
            signature: ours
        ))
    }

    func testX3DHAgreesFromBothSides() throws {
        let x = try loadVectors().x3dh
        let bundle = PreKeyBundle(
            identityKey: x.bobIdentity.public,
            signingKey: x.bobSigning.public,
            signedPreKey: x.bobSignedPrekey.public,
            signedPreKeySignature: x.signedPrekeySignature,
            oneTimePreKey: x.bobOneTimePrekey.public
        )
        let alice = InitiatorKeys(identity: x.aliceIdentity.keyPair, ephemeral: x.aliceEphemeral.keyPair)
        let initiated = try X3DH.initiator(keys: alice, bundle: bundle)
        XCTAssertEqual(initiated.sharedSecret, x.sharedSecret)

        let bob = ResponderKeys(
            identity: x.bobIdentity.keyPair,
            signedPreKey: x.bobSignedPrekey.keyPair,
            oneTimePreKey: x.bobOneTimePrekey.keyPair
        )
        XCTAssertEqual(try X3DH.responder(
            keys: bob,
            initiatorIdentity: x.aliceIdentity.public,
            initiatorEphemeral: initiated.ephemeralPublicKey,
            usedOneTime: true
        ), x.sharedSecret)
        XCTAssertEqual(try X3DH.responder(
            keys: bob,
            initiatorIdentity: x.aliceIdentity.public,
            initiatorEphemeral: initiated.ephemeralPublicKey,
            usedOneTime: false
        ), x.sharedSecretNoOneTime)
    }

    func testReproducesTheWholeConversationByteForByte() throws {
        let v = try loadVectors()
        let aliceKeys = Recorded(v.conversation.aliceRatchetKeys)
        let bobKeys = Recorded(v.conversation.bobRatchetKeys)
        let alice = try XCTUnwrap(RatchetSession.initiator(
            sharedSecret: v.x3dh.sharedSecret,
            theirSignedPreKey: v.x3dh.bobSignedPrekey.public,
            keySource: aliceKeys.next
        ))
        let bob = RatchetSession.responder(
            sharedSecret: v.x3dh.sharedSecret,
            signedPreKey: v.x3dh.bobSignedPrekey.keyPair,
            keySource: bobKeys.next
        )
        let sessions = ["alice": alice, "bob": bob]
        var sent: [String: VecStep] = [:]

        for (index, step) in v.conversation.steps.enumerated() {
            let place = "step \(index) (\(step.op) \(step.id))"
            if step.op == "send" {
                let session = try XCTUnwrap(sessions[step.from ?? ""], place)
                let (header, ciphertext) = try session.encrypt(Data((step.plaintext ?? "").utf8))
                XCTAssertEqual(
                    String(decoding: RatchetHeaderCodec.marshal(header), as: UTF8.self),
                    step.header, place
                )
                XCTAssertEqual(ciphertext, step.ciphertext, place)
                sent[step.id] = step
                continue
            }
            let message = try XCTUnwrap(sent[step.id], place)
            let session = try XCTUnwrap(sessions[step.to ?? ""], place)
            let header = try XCTUnwrap(RatchetHeaderCodec.unmarshal(Data((message.header ?? "").utf8)), place)
            var ciphertext = try XCTUnwrap(message.ciphertext, place)
            if step.op == "forged" {
                ciphertext[ciphertext.index(before: ciphertext.endIndex)] ^= 0x01
                XCTAssertThrowsError(try session.decrypt(header: header, ciphertext: ciphertext), place)
            } else {
                let plaintext = try session.decrypt(header: header, ciphertext: ciphertext)
                XCTAssertEqual(String(decoding: plaintext, as: UTF8.self), message.plaintext ?? "", place)
            }
        }
        XCTAssertEqual(aliceKeys.drawn, v.conversation.aliceRatchetKeys.count)
        XCTAssertEqual(bobKeys.drawn, v.conversation.bobRatchetKeys.count)
    }

    func testComputesTheSameSafetyNumbers() throws {
        for s in try loadVectors().safety {
            let number = Safety.number(
                local: Safety.Identity(
                    stableID: s.localId, identityKey: s.localIdentityKey, signingKey: s.localSigningKey),
                remote: Safety.Identity(
                    stableID: s.remoteId, identityKey: s.remoteIdentityKey, signingKey: s.remoteSigningKey)
            )
            XCTAssertEqual(number, s.number)
        }
    }
}
