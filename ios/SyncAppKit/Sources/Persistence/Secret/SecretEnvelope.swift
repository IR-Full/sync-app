import Foundation
import SyncAppCrypto

/// The X3DH bootstrap that rides inside `SecretMsg.ratchetHeader`.
///
/// The relay carries a ratchet header and a ciphertext and nothing else, but a
/// responder needs two more things on the FIRST message of a session: the initiator's
/// identity key and the ephemeral key it agreed with. Neither has a field on the wire,
/// so they travel in a JSON envelope in the header slot — which is also why the header
/// is text rather than bytes in the legacy encoding.
///
/// The shape is fixed by the web and Go implementations, which already speak it:
///
///     {"ik":"<base64 identity>","ek":"<base64 ephemeral>","rh":"<base64 header>"}
///
/// `ik` and `ek` are absent on every message after the first. `rh` is base64 of
/// `RatchetHeaderCodec.marshal` output — itself JSON, so this is JSON inside base64
/// inside JSON. Wasteful, and not something to "fix" unilaterally: the envelope is
/// authenticated as the AEAD's additional data, so changing its bytes on one platform
/// makes every message from it undecryptable everywhere else.
struct SecretEnvelope: Equatable {
    /// Initiator identity public key, base64. First message only.
    var identityKey: String?
    /// Initiator ephemeral public key, base64. First message only.
    var ephemeralKey: String?
    /// The marshalled ratchet header, base64.
    var ratchetHeader: String

    /// Hand-built, in the field order the other implementations emit.
    ///
    /// Not `JSONEncoder`: these bytes are authenticated, so key order and spacing are
    /// part of the contract, and an encoder is free to change either without changing
    /// the meaning. The same reasoning as `RatchetHeaderCodec.marshal`, one layer out.
    func encoded() -> Data {
        var parts: [String] = []
        if let identityKey { parts.append("\"ik\":\"\(identityKey)\"") }
        if let ephemeralKey { parts.append("\"ek\":\"\(ephemeralKey)\"") }
        parts.append("\"rh\":\"\(ratchetHeader)\"")
        return Data("{\(parts.joined(separator: ","))}".utf8)
    }

    /// Parses one. Returns nil for anything malformed rather than throwing: this data
    /// is relay-supplied, and one bad frame must not take the connection down.
    static func decode(_ bytes: Data) -> SecretEnvelope? {
        guard
            let object = try? JSONSerialization.jsonObject(with: bytes) as? [String: Any],
            let ratchetHeader = object["rh"] as? String,
            !ratchetHeader.isEmpty
        else { return nil }
        return SecretEnvelope(
            identityKey: object["ik"] as? String,
            ephemeralKey: object["ek"] as? String,
            ratchetHeader: ratchetHeader
        )
    }

    /// The ratchet header this envelope carries.
    var header: RatchetHeader? {
        guard let bytes = B64.decode(ratchetHeader) else { return nil }
        return RatchetHeaderCodec.unmarshal(bytes)
    }

    /// The X3DH material, present only on a session's first message.
    var bootstrap: (identity: Data, ephemeral: Data)? {
        guard
            let identityKey, let ephemeralKey,
            let identity = B64.decode(identityKey),
            let ephemeral = B64.decode(ephemeralKey)
        else { return nil }
        return (identity, ephemeral)
    }
}
