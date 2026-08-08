import Foundation
import Network

/// The protocol over a raw TCP stream (`Network.framework`), matching the
/// gateway's `:7000` listener.
///
/// A stream has no message boundaries, so unlike the WebSocket path this one
/// must do the length-prefixed reassembly itself: read the fixed 8-byte header,
/// trust its bounded length field, then read exactly that many payload bytes.
/// That is the entire reason the frame header exists.
///
/// Worth having alongside WebSocket: no per-frame masking or HTTP upgrade, and
/// `NWConnection` reports interface changes (Wi-Fi → LTE) directly, so the
/// reconnect loop learns about a dead link sooner than a socket timeout would
/// tell it.
public actor TCPTransport: Transport {
    private let host: String
    private let port: UInt16
    private let useTLS: Bool
    private let allowSelfSignedCertificates: Bool
    private var connection: NWConnection?

    public init(host: String, port: UInt16, useTLS: Bool, allowSelfSignedCertificates: Bool = false) {
        self.host = host
        self.port = port
        self.useTLS = useTLS
        self.allowSelfSignedCertificates = allowSelfSignedCertificates
    }

    public func connect() async throws {
        let parameters: NWParameters
        if useTLS {
            let tls = NWProtocolTLS.Options()
            sec_protocol_options_set_min_tls_protocol_version(tls.securityProtocolOptions, .TLSv13)
            if allowSelfSignedCertificates {
                // Dev only — the server's `SYNAPSE_TLS_SELFSIGNED=1` mode. The
                // build configuration decides this; it is never on in Release.
                sec_protocol_options_set_verify_block(
                    tls.securityProtocolOptions,
                    { _, _, complete in complete(true) },
                    DispatchQueue.global()
                )
            }
            parameters = NWParameters(tls: tls, tcp: .init())
        } else {
            parameters = NWParameters(tls: nil, tcp: .init())
        }

        guard let nwPort = NWEndpoint.Port(rawValue: port) else {
            throw TransportError.invalidURL("\(host):\(port)")
        }
        let connection = NWConnection(host: .init(host), port: nwPort, using: parameters)
        self.connection = connection

        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            // `stateUpdateHandler` fires more than once; the box makes sure the
            // continuation is resumed exactly once regardless.
            let resumed = ResumeOnce(continuation)
            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    resumed.succeed()
                case .failed(let error):
                    resumed.fail(TransportError.connectionFailed(error.localizedDescription))
                case .cancelled:
                    resumed.fail(TransportError.connectionClosed)
                default:
                    break
                }
            }
            connection.start(queue: .global(qos: .userInitiated))
        }
    }

    public func send(payload: Data) async throws {
        guard let connection else { throw TransportError.notConnected }
        let frame = try Frame.encode(payload: payload)
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.send(content: frame, completion: .contentProcessed { error in
                if let error {
                    continuation.resume(throwing: TransportError.connectionFailed(error.localizedDescription))
                } else {
                    continuation.resume()
                }
            })
        }
    }

    public func receive() async throws -> Data {
        let header = try await readExactly(Frame.headerSize)
        let (flags, length) = try Frame.parseHeader([UInt8](header))
        if flags & (FrameFlag.compressed | FrameFlag.zstd) != 0 {
            throw WireError.unsupportedCompression(flags)
        }
        guard length > 0 else { return Data() }
        return try await readExactly(length)
    }

    public func close() async {
        connection?.cancel()
        connection = nil
    }

    /// `NWConnection` may deliver a short read; `minimumIncompleteLength` makes
    /// it wait for the full count instead, so a frame never arrives in pieces.
    private func readExactly(_ count: Int) async throws -> Data {
        guard let connection else { throw TransportError.notConnected }
        return try await withCheckedThrowingContinuation { continuation in
            connection.receive(minimumIncompleteLength: count, maximumLength: count) { data, _, isComplete, error in
                if let error {
                    continuation.resume(throwing: TransportError.connectionFailed(error.localizedDescription))
                    return
                }
                guard let data, data.count == count else {
                    continuation.resume(throwing: isComplete
                        ? TransportError.connectionClosed
                        : TransportError.connectionFailed("short read"))
                    return
                }
                continuation.resume(returning: data)
            }
        }
    }
}

/// Guards a continuation that a repeatedly-firing callback might resume twice —
/// which is a crash, not a warning.
private final class ResumeOnce: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Void, Error>?

    init(_ continuation: CheckedContinuation<Void, Error>) {
        self.continuation = continuation
    }

    func succeed() { take()?.resume() }
    func fail(_ error: Error) { take()?.resume(throwing: error) }

    private func take() -> CheckedContinuation<Void, Error>? {
        lock.lock()
        defer { lock.unlock() }
        let continuation = self.continuation
        self.continuation = nil
        return continuation
    }
}
