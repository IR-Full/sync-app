import Foundation

/// The protocol over a WebSocket — one binary message carries exactly one frame.
///
/// This is the default transport. `URLSession` gives us system TLS, proxy and
/// captive-portal handling, and cellular/Wi-Fi interface selection for free, and
/// `/ws` survives networks that silently drop the raw `:7000` listener.
///
/// The task is an actor because `URLSessionWebSocketTask` tolerates concurrent
/// `send` calls but not concurrent `receive` calls, and the ordering of frames
/// on the wire is load-bearing for `Envelope.seq`.
public actor WebSocketTransport: Transport {
    private let url: URL
    private let session: URLSession
    private let openDelegate: OpenDelegate
    private var task: URLSessionWebSocketTask?

    public init(url: URL, configuration: URLSessionConfiguration = .ephemeral) {
        self.url = url
        self.openDelegate = OpenDelegate()
        // Waiting for connectivity turns "no network right now" into a delayed
        // connect instead of an immediate failure — which is the behaviour the
        // reconnect loop wants on a phone coming out of a tunnel.
        configuration.waitsForConnectivity = true
        self.session = URLSession(configuration: configuration, delegate: openDelegate, delegateQueue: nil)
    }

    public func connect() async throws {
        let task = session.webSocketTask(with: url)
        self.task = task
        let opened = openDelegate.expectOpen(for: task)
        task.resume()
        try await opened
    }

    public func send(payload: Data) async throws {
        guard let task else { throw TransportError.notConnected }
        let frame = try Frame.encode(payload: payload)
        try await task.send(.data(frame))
    }

    public func receive() async throws -> Data {
        guard let task else { throw TransportError.notConnected }
        while true {
            switch try await task.receive() {
            case .data(let data):
                return try Frame.decode(data)
            case .string:
                // The protocol only uses binary messages; the gateway ignores
                // text frames on its side, so we do the same rather than tearing
                // the connection down over a stray one.
                continue
            @unknown default:
                continue
            }
        }
    }

    public func close() async {
        task?.cancel(with: .goingAway, reason: nil)
        task = nil
        openDelegate.cancelPending()
    }
}

/// Bridges `URLSessionWebSocketDelegate`'s open/close callbacks to `async`.
///
/// Without this, `connect()` would resolve the moment `resume()` is called and
/// the first real failure would surface as a confusing `receive()` error several
/// steps into the handshake.
private final class OpenDelegate: NSObject, URLSessionWebSocketDelegate, @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Void, Error>?
    private var pendingTaskID: Int?

    func expectOpen(for task: URLSessionWebSocketTask) async throws {
        try await withCheckedThrowingContinuation { continuation in
            lock.lock()
            self.continuation = continuation
            self.pendingTaskID = task.taskIdentifier
            lock.unlock()
        }
    }

    func cancelPending() {
        resume(with: .failure(TransportError.connectionClosed))
    }

    private func resume(with result: Result<Void, Error>) {
        lock.lock()
        let continuation = self.continuation
        self.continuation = nil
        self.pendingTaskID = nil
        lock.unlock()
        continuation?.resume(with: result)
    }

    func urlSession(
        _ session: URLSession,
        webSocketTask: URLSessionWebSocketTask,
        didOpenWithProtocol protocolName: String?
    ) {
        resume(with: .success(()))
    }

    func urlSession(
        _ session: URLSession,
        webSocketTask: URLSessionWebSocketTask,
        didCloseWith closeCode: URLSessionWebSocketTask.CloseCode,
        reason: Data?
    ) {
        resume(with: .failure(TransportError.connectionClosed))
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard let error else { return }
        resume(with: .failure(TransportError.connectionFailed(error.localizedDescription)))
    }
}
