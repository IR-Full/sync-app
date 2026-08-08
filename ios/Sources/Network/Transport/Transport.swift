import Foundation

/// A bidirectional stream of protocol *frames*.
///
/// The protocol is transport-agnostic by design — the gateway speaks the same
/// framing over raw TCP, WebSocket and QUIC — so the client is written against
/// this and nothing else. `SynapseClient` never imports a socket type.
///
/// Implementations must deliver exactly one frame payload (the envelope bytes,
/// header already stripped) per `receive()`.
public protocol Transport: AnyObject, Sendable {
    /// Opens the connection. Must not return until frames can be sent.
    func connect() async throws

    /// Sends one already-framed payload. Implementations add the frame header.
    func send(payload: Data) async throws

    /// Returns the next frame's payload, or throws when the connection ends.
    func receive() async throws -> Data

    /// Closes the connection; subsequent `receive()` calls must throw.
    func close() async
}

/// Transport selection. The server exposes all three; this is a config choice,
/// not a code change.
public enum TransportKind: String, Sendable, Codable, CaseIterable {
    /// `/ws` on the HTTP listener. The default: it traverses corporate proxies
    /// and captive portals that drop unknown TCP ports, and `URLSession` gives
    /// us system-managed TLS, background handling and cellular fallback.
    case webSocket

    /// The raw binary listener (`:7000`) over `Network.framework`. Lower
    /// overhead per frame (no WS masking/fragmentation), and worth using on a
    /// controlled network.
    case tcp
}

/// Errors a transport can raise before any protocol error exists.
public enum TransportError: Error, Equatable, Sendable {
    case notConnected
    case connectionClosed
    case connectionFailed(String)
    case invalidURL(String)
    case receivedTextFrame
}
