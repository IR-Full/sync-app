import CryptoKit
import Foundation

/// The Double Ratchet, ported from `server/pkg/e2e/ratchet.go`.
///
/// Forward secrecy (a leaked key does not open past messages) plus
/// post-compromise security (a DH ratchet step heals the session). The server is
/// a blind relay throughout — it moves ciphertext and a header and can decrypt
/// neither.
///
/// The header is authenticated, not encrypted: it travels as additional data for
/// the AEAD, so tampering with the ratchet key or counters fails decryption.

/// Bounds the work a hostile header can force by claiming a huge gap, per call.
private let maxSkip = 1000

/// Bounds how many skipped message keys a session RETAINS.
///
/// `maxSkip` only ever bounded a single call: every DH ratchet step restarts the
/// count, so the map would grow without limit — and this state is persisted, so
/// the growth would outlive the process.
private let maxSkippedKeys = 2 * maxSkip

private let rootKeyInfo = Data("SyncApp-Ratchet-RK".utf8)
private let messageKeyInfo = Data("SyncApp-Ratchet-Msg".utf8)

/// Travels (authenticated but not encrypted) with each ciphertext.
public struct RatchetHeader: Equatable, Sendable {
    /// Sender's current ratchet public key.
    public let dh: Data
    /// Number of messages in the previous sending chain.
    public let pn: Int
    /// Message number in the current sending chain.
    public let n: Int

    /// The exact serialisation this header travels as — the bytes
    /// ``RatchetHeaderCodec/unmarshal(_:)`` parsed it from, or the bytes
    /// ``RatchetSession/encrypt(_:)`` authenticated. Nil for a header built from
    /// values alone, which ``RatchetHeaderCodec/marshal(_:)`` then encodes
    /// canonically.
    ///
    /// This makes the AEAD's additional data literally the bytes on the wire, in
    /// both directions, rather than a re-encoding that merely ought to match
    /// them. Four implementations produce this header — Go, TypeScript, Kotlin,
    /// Swift — and each receiver used to parse the header and then re-serialise
    /// it to rebuild the AD. That works only while all four emit byte-identical
    /// canonical JSON: same field order, no whitespace, the same base64 alphabet
    /// and padding. Nothing enforces it, and the day one diverges every message
    /// between the two versions fails with ``RatchetError/decryptionFailed`` —
    /// indistinguishable from a forgery, and pointing at nothing. Carrying the
    /// bytes removes the requirement instead of documenting it.
    ///
    /// Left out of the public initialiser, and that is the safety property, not
    /// a style choice. The bytes and the parsed counters must never disagree:
    /// the ratchet drives its state from `dh`/`pn`/`n` while the AEAD verifies
    /// the bytes, so a header whose fields say one thing and whose bytes say
    /// another would advance the session on values nobody authenticated. A
    /// header rebuilt as `RatchetHeader(dh: h.dh, pn: 99, n: h.n)` therefore
    /// starts with no bytes of its own and is encoded from the counters it
    /// actually carries — and the AEAD rejects it. `Header.raw` in
    /// `server/pkg/e2e/ratchet.types.go` gets the same guarantee from being
    /// unexported.
    public fileprivate(set) var wireBytes: Data?

    public init(dh: Data, pn: Int, n: Int) {
        self.dh = dh
        self.pn = pn
        self.n = n
        self.wireBytes = nil
    }

    /// Equality covers the three wire fields only. ``wireBytes`` is derived from
    /// them for any header that has it, so including it would either be
    /// redundant or would make a parsed header unequal to the same header built
    /// from its values.
    public static func == (lhs: RatchetHeader, rhs: RatchetHeader) -> Bool {
        lhs.dh == rhs.dh && lhs.pn == rhs.pn && lhs.n == rhs.n
    }
}

public enum RatchetError: Error, Equatable, Sendable {
    case decryptionFailed
    /// A send was attempted on a session that has never ratcheted.
    case noSendingChain
}

/// A session serialised for persistence across restarts.
public struct SerializedSession: Codable, Equatable, Sendable {
    public var dhsPrivate: String
    public var dhsPublic: String
    public var dhr: String?
    public var rootKey: String
    public var sendingChainKey: String?
    public var receivingChainKey: String?
    public var sent: Int
    public var received: Int
    public var previousSent: Int
    public var skipped: [String: String]
    /// Insertion order of `skipped`, so the oldest key is the one evicted.
    public var skippedOrder: [String]

    public init(
        dhsPrivate: String,
        dhsPublic: String,
        dhr: String? = nil,
        rootKey: String,
        sendingChainKey: String? = nil,
        receivingChainKey: String? = nil,
        sent: Int = 0,
        received: Int = 0,
        previousSent: Int = 0,
        skipped: [String: String] = [:],
        skippedOrder: [String] = []
    ) {
        self.dhsPrivate = dhsPrivate
        self.dhsPublic = dhsPublic
        self.dhr = dhr
        self.rootKey = rootKey
        self.sendingChainKey = sendingChainKey
        self.receivingChainKey = receivingChainKey
        self.sent = sent
        self.received = received
        self.previousSent = previousSent
        self.skipped = skipped
        self.skippedOrder = skippedOrder
    }
}

public enum RatchetHeaderCodec {
    /// The header's wire form, and therefore the AEAD's additional data.
    ///
    /// A header that carries its own bytes (``RatchetHeader/wireBytes``) is
    /// returned as it stands: one that arrived keeps what the peer signed, one
    /// this process built keeps what it authenticated. Only a header with none
    /// is encoded here, and that encoding matches Go's `json.Marshal` — standard
    /// base64 for the key, fields in declaration order (dh, pn, n), no
    /// whitespace. Matching still matters for a header this side originates, but
    /// it is no longer the only thing holding interop together.
    ///
    /// Hand-built rather than handed to `JSONEncoder` on purpose — an encoder is
    /// free to reorder keys, add spacing or escape differently, and any of those
    /// changes the authenticated bytes without changing the meaning.
    public static func marshal(_ header: RatchetHeader) -> Data {
        if let wireBytes = header.wireBytes { return wireBytes }
        return Data(#"{"dh":"\#(B64.encode(header.dh))","pn":\#(header.pn),"n":\#(header.n)}"#.utf8)
    }

    /// Parses a header from the wire, remembering the bytes it came from so they
    /// — and not a re-encoding of them — are what gets authenticated. Returns
    /// nil for anything malformed.
    public static func unmarshal(_ bytes: Data) -> RatchetHeader? {
        guard let text = String(data: bytes, encoding: .utf8) else { return nil }
        guard
            let dhText = capture(#""dh"\s*:\s*"([^"]*)""#, in: text),
            let dh = B64.decode(dhText)
        else { return nil }

        // Absent counters read as 0, matching Go's zero value for an omitted
        // field; a NEGATIVE one is malformed and must not become a huge gap
        // after the unsigned conversion the skip loop does.
        let pn = capture(#""pn"\s*:\s*(-?\d+)"#, in: text).flatMap(Int.init) ?? 0
        let n = capture(#""n"\s*:\s*(-?\d+)"#, in: text).flatMap(Int.init) ?? 0
        guard pn >= 0, n >= 0 else { return nil }

        var header = RatchetHeader(dh: dh, pn: pn, n: n)
        header.wireBytes = bytes
        return header
    }

    private static func capture(_ pattern: String, in text: String) -> String? {
        guard
            let regex = try? NSRegularExpression(pattern: pattern),
            let match = regex.firstMatch(
                in: text, range: NSRange(text.startIndex..., in: text)),
            let range = Range(match.range(at: 1), in: text)
        else { return nil }
        return String(text[range])
    }
}

/// Supplies the key pairs a session ratchets to. Tests pass recorded keys.
public typealias RatchetKeySource = () -> SecretKeyPair

/// One Double Ratchet session with one peer device.
///
/// A reference type, and not safe for concurrent use — callers serialise per
/// session, exactly as the Go implementation requires.
public final class RatchetSession {
    private var dhs: SecretKeyPair
    private var dhr: Data?
    private var rootKey: Data
    private var sendingChainKey: Data?
    private var receivingChainKey: Data?
    private var sent = 0
    private var received = 0
    private var previousSent = 0
    private var skipped: [String: Data] = [:]
    private var skippedOrder: [String] = []
    private var keySource: RatchetKeySource = { Crypto.generateKeyPair() }

    private init(dhs: SecretKeyPair, dhr: Data?, rootKey: Data) {
        self.dhs = dhs
        self.dhr = dhr
        self.rootKey = rootKey
    }

    /// Initiator's session. The peer's signed prekey is the first DHr, and one
    /// DH ratchet runs immediately so the initiator can send straight away.
    public static func initiator(
        sharedSecret: Data,
        theirSignedPreKey: Data,
        keySource: @escaping RatchetKeySource = { Crypto.generateKeyPair() }
    ) -> RatchetSession? {
        let session = RatchetSession(
            dhs: keySource(),
            dhr: theirSignedPreKey,
            rootKey: sharedSecret
        )
        session.keySource = keySource
        guard let dhOut = Crypto.diffieHellman(
            privateKey: session.dhs.privateKey, publicKey: theirSignedPreKey)
        else { return nil }

        let (rk, ck) = kdfRootKey(rootKey: session.rootKey, dhOut: dhOut)
        session.rootKey = rk
        session.sendingChainKey = ck
        return session
    }

    /// Responder's session. It has no sending chain until the first message
    /// arrives and triggers a ratchet step.
    public static func responder(
        sharedSecret: Data,
        signedPreKey: SecretKeyPair,
        keySource: @escaping RatchetKeySource = { Crypto.generateKeyPair() }
    ) -> RatchetSession {
        let session = RatchetSession(dhs: signedPreKey, dhr: nil, rootKey: sharedSecret)
        session.keySource = keySource
        return session
    }

    public static func deserialize(_ state: SerializedSession) -> RatchetSession? {
        guard
            let dhsPrivate = B64.decode(state.dhsPrivate),
            let dhsPublic = B64.decode(state.dhsPublic),
            let rootKey = B64.decode(state.rootKey)
        else { return nil }

        let session = RatchetSession(
            dhs: SecretKeyPair(privateKey: dhsPrivate, publicKey: dhsPublic),
            dhr: state.dhr.flatMap(B64.decode),
            rootKey: rootKey
        )
        session.sendingChainKey = state.sendingChainKey.flatMap(B64.decode)
        session.receivingChainKey = state.receivingChainKey.flatMap(B64.decode)
        session.sent = state.sent
        session.received = state.received
        session.previousSent = state.previousSent
        for (key, value) in state.skipped {
            if let decoded = B64.decode(value) { session.skipped[key] = decoded }
        }
        // A session persisted before eviction existed has no recorded order.
        // Falling back to the map's keys is NOT equivalent to Kotlin's insertion
        // order — a Swift dictionary has none — so the fallback is sorted by
        // message number, which is the order those keys were created in and the
        // order to evict in.
        session.skippedOrder = state.skippedOrder.isEmpty
            ? session.skipped.keys.sorted { messageNumber($0) < messageNumber($1) }
            : state.skippedOrder
        return session
    }

    // MARK: - Key derivation

    /// Derives (rootKey, chainKey) from the root key and a DH output.
    private static func kdfRootKey(rootKey: Data, dhOut: Data) -> (Data, Data) {
        let out = Crypto.hkdf(ikm: dhOut, salt: rootKey, info: rootKeyInfo, length: 64)
        return (out.prefix(32), out.suffix(32))
    }

    /// Advances a chain key; the constants 0x02/0x01 are part of the contract.
    private static func kdfChainKey(_ chainKey: Data) -> (next: Data, messageKey: Data) {
        let next = Crypto.hmacSHA256(key: chainKey, message: Data([0x02]))
        let messageKey = Crypto.hmacSHA256(key: chainKey, message: Data([0x01]))
        return (next, messageKey)
    }

    /// Derives the AEAD key and nonce from a message key.
    ///
    /// A fixed nonce is safe here precisely because the key is derived per
    /// message and never reused — the message key itself is deliberately not
    /// used as the cipher key.
    private static func aead(for messageKey: Data) -> (key: SymmetricKey, nonce: Data) {
        let buf = Crypto.hkdf(ikm: messageKey, salt: nil, info: messageKeyInfo, length: 32 + 12)
        return (SymmetricKey(data: buf.prefix(32)), buf.suffix(12))
    }

    private static func seal(messageKey: Data, ad: Data, plaintext: Data) -> Data? {
        let (key, nonceBytes) = aead(for: messageKey)
        guard
            let nonce = try? ChaChaPoly.Nonce(data: nonceBytes),
            let box = try? ChaChaPoly.seal(plaintext, using: key, nonce: nonce, authenticating: ad)
        else { return nil }
        // Go's Seal appends the tag to the ciphertext and returns that; CryptoKit
        // keeps them apart. `ciphertext + tag` is the layout the other three
        // implementations put on the wire.
        return box.ciphertext + box.tag
    }

    private static func open(messageKey: Data, ad: Data, ciphertext: Data) -> Data? {
        let (key, nonceBytes) = aead(for: messageKey)
        // 16 bytes of Poly1305 tag. A frame shorter than that carries no tag at
        // all, and SealedBox would reject it anyway — checking here keeps the
        // arithmetic below from going negative first.
        guard ciphertext.count >= 16, let nonce = try? ChaChaPoly.Nonce(data: nonceBytes) else {
            return nil
        }
        let body = ciphertext.prefix(ciphertext.count - 16)
        let tag = ciphertext.suffix(16)
        guard
            let box = try? ChaChaPoly.SealedBox(nonce: nonce, ciphertext: body, tag: tag),
            let plaintext = try? ChaChaPoly.open(box, using: key, authenticating: ad)
        else { return nil }
        return plaintext
    }

    /// Storage key for a skipped message key. Internal only — never on the wire.
    private static func skippedKey(dhPublic: Data, n: Int) -> String {
        "\(B64.encode(dhPublic))|\(n)"
    }

    /// Reads the message number back out of a storage key, for ordering.
    private static func messageNumber(_ key: String) -> Int {
        Int(key.split(separator: "|").last.map(String.init) ?? "") ?? 0
    }

    // MARK: - Messaging

    public func encrypt(_ plaintext: Data) throws -> (header: RatchetHeader, ciphertext: Data) {
        guard let chain = sendingChainKey else { throw RatchetError.noSendingChain }
        let (nextChain, messageKey) = Self.kdfChainKey(chain)
        sendingChainKey = nextChain

        var header = RatchetHeader(dh: dhs.publicKey, pn: previousSent, n: sent)
        sent += 1
        // Pin the serialisation used as additional data to this header, so whatever
        // the caller puts on the wire is byte-for-byte what this AEAD authenticated.
        let ad = RatchetHeaderCodec.marshal(header)
        header.wireBytes = ad
        guard let ciphertext = Self.seal(
            messageKey: messageKey,
            ad: ad,
            plaintext: plaintext
        ) else { throw RatchetError.decryptionFailed }
        return (header, ciphertext)
    }

    /// Opens one inbound message.
    ///
    /// **No state moves until the AEAD says the message is genuine.** That
    /// ordering is the security property, not a tidiness preference: the header
    /// is written by whoever sent the frame, and the relay lets any account
    /// address any device. Ratcheting first and authenticating afterwards means
    /// a single forged frame carrying a random ratchet key rewrites the session
    /// — breaking the real conversation and costing up to `2 * maxSkip` HMAC
    /// derivations on the way.
    public func decrypt(header: RatchetHeader, ciphertext: Data) throws -> Data {
        // 1. A key stored for a message that arrived out of order. Consuming one
        //    is already commit-on-success, so it needs no staging.
        if let plaintext = trySkipped(header: header, ciphertext: ciphertext) {
            return plaintext
        }

        // 2. The ordinary case: the peer's current ratchet key, the next message
        //    in the chain. Only the receiving chain moves, so it is held in
        //    locals until the message authenticates — and this branch, which
        //    every in-order message takes, never pays to copy the skipped map.
        if let chain = receivingChainKey, sameDHR(header.dh), header.n == received {
            let (nextChain, messageKey) = Self.kdfChainKey(chain)
            guard let plaintext = Self.open(
                messageKey: messageKey,
                ad: RatchetHeaderCodec.marshal(header),
                ciphertext: ciphertext
            ) else { throw RatchetError.decryptionFailed }
            receivingChainKey = nextChain
            received += 1
            return plaintext
        }

        // 3. Everything else — a DH ratchet step, a gap to skip, or both — runs
        //    on a COPY, adopted only if the frame authenticates.
        guard let trial = Self.deserialize(serialize()) else {
            throw RatchetError.decryptionFailed
        }
        trial.keySource = keySource
        let plaintext = try trial.advance(header: header, ciphertext: ciphertext)
        adopt(trial)
        return plaintext
    }

    /// The ratchet+skip path, run against a throwaway copy by `decrypt`. It may
    /// mutate freely: nothing it touches is the caller's session until `adopt`.
    private func advance(header: RatchetHeader, ciphertext: Data) throws -> Data {
        if !sameDHR(header.dh) {
            try skipMessageKeys(until: header.pn)
            try dhRatchet(header)
        }
        try skipMessageKeys(until: header.n)

        guard let chain = receivingChainKey else { throw RatchetError.decryptionFailed }
        let (nextChain, messageKey) = Self.kdfChainKey(chain)
        receivingChainKey = nextChain
        received += 1
        guard let plaintext = Self.open(
            messageKey: messageKey,
            ad: RatchetHeaderCodec.marshal(header),
            ciphertext: ciphertext
        ) else { throw RatchetError.decryptionFailed }
        return plaintext
    }

    /// Takes over a successful trial's state.
    private func adopt(_ trial: RatchetSession) {
        dhs = trial.dhs
        dhr = trial.dhr
        rootKey = trial.rootKey
        sendingChainKey = trial.sendingChainKey
        receivingChainKey = trial.receivingChainKey
        sent = trial.sent
        received = trial.received
        previousSent = trial.previousSent
        skipped = trial.skipped
        skippedOrder = trial.skippedOrder
    }

    private func sameDHR(_ dhPublic: Data) -> Bool {
        guard let dhr else { return false }
        return dhr == dhPublic
    }

    /// Advances to the peer's new ratchet key: new receiving chain, then sending.
    private func dhRatchet(_ header: RatchetHeader) throws {
        previousSent = sent
        sent = 0
        received = 0
        dhr = header.dh

        guard let dhIn = Crypto.diffieHellman(
            privateKey: dhs.privateKey, publicKey: header.dh)
        else { throw RatchetError.decryptionFailed }
        let (rk1, ckr) = Self.kdfRootKey(rootKey: rootKey, dhOut: dhIn)
        rootKey = rk1
        receivingChainKey = ckr

        dhs = keySource()
        guard let dhOut = Crypto.diffieHellman(
            privateKey: dhs.privateKey, publicKey: header.dh)
        else { throw RatchetError.decryptionFailed }
        let (rk2, cks) = Self.kdfRootKey(rootKey: rootKey, dhOut: dhOut)
        rootKey = rk2
        sendingChainKey = cks
    }

    /// Stores keys for messages not seen yet, so a late arrival still opens.
    private func skipMessageKeys(until: Int) throws {
        guard let chain = receivingChainKey, let peer = dhr else { return }
        if until - received > maxSkip { throw RatchetError.decryptionFailed }

        var current = chain
        while received < until {
            let (next, messageKey) = Self.kdfChainKey(current)
            current = next
            storeSkipped(key: Self.skippedKey(dhPublic: peer, n: received), messageKey: messageKey)
            received += 1
        }
        receivingChainKey = current
    }

    /// Records a message key for a gap, evicting the oldest once the store is
    /// full.
    ///
    /// A bound has to drop something, and the oldest is the right thing to drop:
    /// a message that has not arrived after thousands of later ones is not
    /// arriving, and being wrong costs one undecryptable message rather than a
    /// session.
    private func storeSkipped(key: String, messageKey: Data) {
        if skipped[key] == nil { skippedOrder.append(key) }
        skipped[key] = messageKey
        while skipped.count > maxSkippedKeys, !skippedOrder.isEmpty {
            skipped.removeValue(forKey: skippedOrder.removeFirst())
        }
    }

    private func trySkipped(header: RatchetHeader, ciphertext: Data) -> Data? {
        let key = Self.skippedKey(dhPublic: header.dh, n: header.n)
        guard let messageKey = skipped[key] else { return nil }
        guard let plaintext = Self.open(
            messageKey: messageKey,
            ad: RatchetHeaderCodec.marshal(header),
            ciphertext: ciphertext
        ) else { return nil }
        // A message key is single-use; keeping it would let a replay succeed.
        // `skippedOrder` keeps its entry: it is an eviction order, not an index,
        // and storeSkipped tolerates naming a key that is already gone.
        skipped.removeValue(forKey: key)
        return plaintext
    }

    public func serialize() -> SerializedSession {
        SerializedSession(
            dhsPrivate: B64.encode(dhs.privateKey),
            dhsPublic: B64.encode(dhs.publicKey),
            dhr: dhr.map(B64.encode),
            rootKey: B64.encode(rootKey),
            sendingChainKey: sendingChainKey.map(B64.encode),
            receivingChainKey: receivingChainKey.map(B64.encode),
            sent: sent,
            received: received,
            previousSent: previousSent,
            skipped: skipped.mapValues(B64.encode),
            skippedOrder: skippedOrder
        )
    }
}
