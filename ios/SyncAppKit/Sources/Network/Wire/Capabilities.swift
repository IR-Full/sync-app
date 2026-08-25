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
    public static let client: Capabilities = [.resume, .typingSignals]
}
