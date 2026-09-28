import Foundation

/// Capability bitset negotiated in `HELLO`/`WELCOME`. The agreed set is the
/// intersection, and unknown bits are ignored — which is what makes an old
/// server and a new client still agree on something workable.
public struct Capabilities: OptionSet, Sendable {
    public let rawValue: UInt32
    public init(rawValue: UInt32) { self.rawValue = rawValue }

    public static let compression = Capabilities(rawValue: 1 << 0)   // gzip frames
    public static let batching = Capabilities(rawValue: 1 << 1)
    public static let resume = Capabilities(rawValue: 1 << 2)
    public static let secretChat = Capabilities(rawValue: 1 << 3)
    public static let typingSignals = Capabilities(rawValue: 1 << 4)
    public static let zstd = Capabilities(rawValue: 1 << 5)          // zstd + shared dict
    /// Peer speaks the DURABLE secret-chat protocol — it acts on `SECRET_ACK`, asks
    /// for what it missed with `SECRET_SYNC`, and confirms with `SECRET_ACKED`. It also
    /// selects the BINARY ratchet payload over base64.
    ///
    /// One bit for both because they shipped together in the same client release; a
    /// separate bit for the encoding would describe a client that never existed.
    public static let secretQueue = Capabilities(rawValue: 1 << 6)

    /// What this client advertises.
    ///
    /// Compression is deliberately absent. The server only compresses when the
    /// *negotiated* set says we can decompress, so not advertising it is what
    /// guarantees every inbound frame is plaintext — and lets `Frame.decode`
    /// treat a compressed frame as the protocol violation it would be. Chat
    /// frames are a few hundred bytes; shipping a zstd decoder with the server's
    /// shared dictionary to save on that is not a trade worth making yet.
    ///
    /// `secretChat` is advertised only when the app is built with the E2E
    /// module; this build relays no ciphertext, so it stays off.
    ///
    /// `secretQueue` IS advertised, and it is the one thing here that is not gated on
    /// `secretChat`. The two mean different things: `secretChat` says "this build
    /// relays ciphertext for chats it did not create", while `secretQueue` says "this
    /// build speaks the durable protocol" — it acts on `SECRET_ACK`, asks for what it
    /// missed with `SECRET_SYNC`, confirms with `SECRET_ACKED`, and reads the binary
    /// payload. `SecretChatService` does all four, and `SecretMsgBody.payload()` reads
    /// either encoding, so the claim is true.
    ///
    /// It matters more than a saved third of a frame: WITHOUT this bit the gateway does
    /// not queue for this device at all, so a secret message sent while the phone is
    /// asleep is dropped and the sender is told nothing.
    /// `batching` is advertised now that `history(chatID:beforeSeq:limit:)` reads
    /// a `HISTORY_PAGE`. The bit is a claim about what this build can DECODE, so
    /// it could not be set before that existed: the gateway takes it as licence
    /// to answer a backfill with one frame instead of a hundred `NEW` frames,
    /// and a client that advertised it without handling the page would sit
    /// waiting for a `HISTORY_OK` that is never coming.
    ///
    /// What it buys is the same page in one envelope rather than up to a hundred
    /// — a hundred protobuf bodies and a hundred trips through the connection's
    /// single writer, to answer one request. Scrolling back through a chat is
    /// the most common thing this app asks the server to do.
    public static let client: Capabilities = [.resume, .typingSignals, .secretQueue, .batching]
}
