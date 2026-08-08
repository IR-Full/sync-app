import Foundation
import OSLog

/// The whole custom-protocol surface, in one place.
///
/// The gateway is not REST. There is one connection, and everything — auth,
/// sending, history, receipts, typing — rides it:
///
///     dial → HELLO/WELCOME (capability negotiation)
///          → AUTH or RESUME (identity)
///          → steady state: request/reply by RequestID + unsolicited pushes
///          → drop → backoff → redial, resuming the session when possible
///
/// This actor owns that lifecycle and nothing else. It knows about frames,
/// envelopes and correlation; it does not know what a chat is. Repositories sit
/// above it and are the ones allowed to have opinions about domain state — which
/// is what keeps `seq`/`ack` bookkeeping and the streamed-history convention
/// from leaking into view models.
public actor SynapseClient {

    // MARK: - Public surface

    public enum ConnectionState: Sendable, Equatable {
        case idle
        case connecting
        case authenticating
        case ready
        case reconnecting
        case closed
    }

    public struct Session: Sendable, Equatable, Codable {
        public var userID: String
        public var deviceID: String
        public var sessionID: String
        public var token: String
        public var resumeToken: String

        public init(userID: String, deviceID: String, sessionID: String, token: String, resumeToken: String) {
            self.userID = userID
            self.deviceID = deviceID
            self.sessionID = sessionID
            self.token = token
            self.resumeToken = resumeToken
        }
    }

    public enum Credentials: Sendable, Equatable {
        case token(String)
        case password(username: String, password: String, register: Bool)
    }

    /// Unsolicited server pushes plus connection-level signals.
    public enum Event: Sendable {
        case state(ConnectionState)
        case message(NewMessageBody)
        case sendAck(SendAckBody)
        case readReceipt(ReadUpdateBody)
        case typing(TypingBody)
        case presence(PresenceBody)
        case reaction(ReactUpdateBody)
        case chatInfo(ChatInfoBody)
        case pinned(PinnedBody)
        /// A draft one of this user's *other* devices changed. Drafts are routed
        /// per-user, never to the chat, so this only ever concerns us.
        case drafts(DraftsBody)
        /// An error frame that correlated to no in-flight request.
        case error(ProtocolError)
        /// The session is gone; the app must send the user back to login.
        case sessionExpired(ProtocolError)
    }

    public struct Configuration: Sendable {
        public var clientVersion: String
        public var platform: String
        public var requestTimeout: Duration
        public var maxBackoff: Duration
        /// Force a redial after this much silence. The gateway pings every
        /// `heartbeatMs` (20s by default) and kills us at 60s of silence, so
        /// anything longer than that is a socket the OS has not noticed is dead
        /// — common on a suspended phone or a flaky link.
        public var livenessTimeout: Duration

        public init(
            clientVersion: String = "ios/1.0",
            platform: String = "ios",
            requestTimeout: Duration = .seconds(15),
            maxBackoff: Duration = .seconds(30),
            livenessTimeout: Duration = .seconds(45)
        ) {
            self.clientVersion = clientVersion
            self.platform = platform
            self.requestTimeout = requestTimeout
            self.maxBackoff = maxBackoff
            self.livenessTimeout = livenessTimeout
        }
    }

    // MARK: - State

    private let environment: ServerEnvironment
    private let configuration: Configuration
    /// How a new socket is made. Injectable so the handshake, correlation and
    /// reconnect logic can be tested against a scripted peer instead of a real
    /// gateway — the alternative is testing none of it.
    private let makeTransport: @Sendable () -> Transport
    private let log = Logger(subsystem: "chat.synapse.ios", category: "protocol")

    private var transport: Transport?
    private var readLoop: Task<Void, Never>?
    private var writeLoop: Task<Void, Never>?
    private var outbound: AsyncStream<Data>.Continuation?
    private var watchdog: Task<Void, Never>?
    private var reconnectTask: Task<Void, Never>?

    private(set) public var state: ConnectionState = .idle {
        didSet {
            guard state != oldValue else { return }
            emit(.state(state))
        }
    }
    private(set) public var session: Session?
    private var credentials: Credentials?
    private var deviceID: String = ""

    /// Our own per-connection monotonic sequence. Resets on every new socket
    /// because the gateway's view of it resets too.
    private var outSeq: UInt64 = 0
    /// Highest server seq seen *on this socket* — piggybacked as `Envelope.ack`.
    private var lastServerSeq: UInt64 = 0
    /// Highest server seq seen on the session, across sockets. This is what
    /// `RESUME` sends, and it must survive the reconnect that resume exists for.
    private var resumeCursor: UInt64 = 0

    private var requestCounter: UInt64 = 0
    private var pending: [UInt64: PendingRequest] = [:]
    private var negotiatedCaps: Capabilities = []
    private var heartbeat: Duration = .seconds(20)

    private var shouldReconnect = false
    private var reconnectAttempts = 0
    private var lastFrameAt = ContinuousClock.now

    private var subscribers: [UUID: AsyncStream<Event>.Continuation] = [:]

    public init(
        environment: ServerEnvironment,
        configuration: Configuration = .init(),
        transportFactory: (@Sendable () -> Transport)? = nil
    ) {
        self.environment = environment
        self.configuration = configuration
        self.makeTransport = transportFactory ?? { environment.makeTransport() }
    }

    // MARK: - Events

    /// A fresh stream of pushes. Multiple callers each get their own; a dropped
    /// consumer unsubscribes itself.
    public func events() -> AsyncStream<Event> {
        let client = self

        return AsyncStream { continuation in
            let id = UUID()

            client.subscribers[id] = continuation

            continuation.onTermination = { [weak client] _ in
                Task { [weak client, id] in
                    await client?.removeSubscriber(id)
                }
            }
        }
    }

    private func removeSubscriber(_ id: UUID) { subscribers[id] = nil }

    private func emit(_ event: Event) {
        for continuation in subscribers.values { continuation.yield(event) }
    }

    // MARK: - Lifecycle

    /// Stable per-installation device id. The gateway keys sessions, multi-device
    /// delivery and push tokens on it, so it must be the same value on every
    /// launch — see `DeviceIdentity` for where it is generated and stored.
    public func setDeviceID(_ id: String) { deviceID = id }

    /// Opens the connection and authenticates, returning once the gateway has
    /// accepted us — "actually usable", not merely "socket open".
    @discardableResult
    public func connect(credentials: Credentials) async throws -> Session {
        self.credentials = credentials
        shouldReconnect = true
        reconnectAttempts = 0
        return try await dial()
    }

    /// Restores a previous session without re-entering a password.
    @discardableResult
    public func connect(session: Session) async throws -> Session {
        self.session = session
        if !session.deviceID.isEmpty { deviceID = session.deviceID }
        return try await connect(credentials: .token(session.token))
    }

    /// Closes for good: no reconnect, every in-flight request fails.
    public func disconnect() async {
        shouldReconnect = false
        reconnectTask?.cancel()
        reconnectTask = nil
        await teardown(error: ClientError.closed)
        state = .closed
    }

    /// Closes *and* forgets the identity. Used by logout, where leaving a resume
    /// token behind would let a background reconnect revive the session.
    public func reset() async {
        await disconnect()
        session = nil
        credentials = nil
        resumeCursor = 0
        state = .idle
    }

    // MARK: - Dial / handshake

    @discardableResult
    private func dial() async throws -> Session {
        state = reconnectAttempts > 0 ? .reconnecting : .connecting
        outSeq = 0
        lastServerSeq = 0

        let transport = makeTransport()
        self.transport = transport
        do {
            try await transport.connect()
        } catch {
            self.transport = nil
            throw error
        }

        lastFrameAt = .now
        startWriteLoop(on: transport)
        startReadLoop(on: transport)
        startWatchdog()

        do {
            let session = try await handshakeAndAuthenticate()
            reconnectAttempts = 0
            state = .ready
            return session
        } catch {
            await teardown(error: error)
            throw error
        }
    }

    private func handshakeAndAuthenticate() async throws -> Session {
        state = .authenticating

        // --- HELLO / WELCOME: capabilities before anything else.
        let hello = HelloBody(
            clientVersion: configuration.clientVersion,
            deviceID: deviceID,
            platform: configuration.platform,
            caps: .client
        )
        let welcomeReply = try await request(.hello, body: hello, expect: .welcome)
        let welcome = try WelcomeBody.protoDecoded(from: welcomeReply.body)
        negotiatedCaps = welcome.caps
        if welcome.heartbeatMs > 0 {
            heartbeat = .milliseconds(Int(welcome.heartbeatMs))
        }

        // --- RESUME, if we have a session and both sides support it. A resume
        // replays the frames we missed instead of making us refetch history.
        let canResume =
            (session?.resumeToken.isEmpty == false)
            && welcome.resumeSupported
            && negotiatedCaps.contains(.resume)

        if canResume, let existing = session {
            do {
                _ = try await request(
                    .resume,
                    body: ResumeBody(resumeToken: existing.resumeToken, lastAckSeq: resumeCursor),
                    expect: .resumeOK
                )
                log.info("session resumed from seq \(self.resumeCursor)")
                return existing
            } catch let error as ProtocolError where error.isAuthFailure {
                // The session is genuinely dead. Re-auth cannot use the token
                // either, so let it propagate and the app will show login.
                throw error
            } catch {
                // Expired replay buffer or a revoked *resume* token. The socket
                // stays usable — the gateway errors, it does not close — and it
                // is now waiting for a fresh AUTH.
                log.notice("resume refused (\(String(describing: error))); falling back to AUTH")
            }
        }

        return try await authenticate()
    }

    private func authenticate() async throws -> Session {
        guard let credentials else { throw ClientError.noCredentials }

        let body: AuthBody
        switch credentials {
        case .token(let token):
            body = AuthBody(token: token)
        case .password(let username, let password, let register):
            body = AuthBody(username: username, password: password, register: register)
        }

        let reply = try await request(.auth, body: body, expect: .authOK)
        let ok = try AuthOKBody.protoDecoded(from: reply.body)

        // A fresh session restarts the server's sequence at 1, so the resume
        // cursor from the previous session must not survive into this one.
        resumeCursor = reply.seq
        lastServerSeq = reply.seq

        let session = Session(
            userID: ok.userID,
            deviceID: ok.deviceID,
            sessionID: ok.sessionID,
            token: ok.token,
            resumeToken: ok.resumeToken
        )
        // The gateway assigns a device id when we sent none; adopt it so the
        // next reconnect presents the same device.
        if !session.deviceID.isEmpty { deviceID = session.deviceID }
        self.session = session
        // Once authenticated a token beats a password for any future reconnect:
        // it survives a password change and keeps credentials out of memory.
        if !session.token.isEmpty { self.credentials = .token(session.token) }
        return session
    }

    // MARK: - Read loop

    private func startReadLoop(on transport: Transport) {
        readLoop?.cancel()
        readLoop = Task { [weak self] in
            while !Task.isCancelled {
                do {
                    let payload = try await transport.receive()
                    await self?.ingest(payload)
                } catch {
                    await self?.handleDisconnect(error)
                    return
                }
            }
        }
    }

    private func ingest(_ payload: Data) async {
        lastFrameAt = .now

        let envelope: Envelope
        do {
            envelope = try Envelope.decode(payload)
        } catch {
            // A malformed frame is the server's bug or an attacker's probe.
            // Dropping it is right; tearing down a healthy connection is not.
            log.error("dropping malformed frame: \(String(describing: error))")
            return
        }

        if envelope.seq > lastServerSeq {
            lastServerSeq = envelope.seq
            if envelope.seq > resumeCursor { resumeCursor = envelope.seq }
        }

        switch envelope.type {
        case .ping:
            // Replying also refreshes our presence TTL server-side: the gateway
            // only bumps it on inbound client frames.
            try? await write(.pong, body: nil, requestID: envelope.requestID)
            return
        case .pong, .transportAck:
            return
        default:
            break
        }

        if envelope.requestID != 0, pending[envelope.requestID] != nil {
            settle(envelope)
            return
        }
        dispatchPush(envelope)
    }

    /// Routes a correlated reply to its awaiting request, accumulating the item
    /// frames of a streamed page.
    private func settle(_ envelope: Envelope) {
        guard var request = pending[envelope.requestID] else { return }

        if envelope.type == .error || envelope.type == .authErr {
            let error = (try? ErrorBody.protoDecoded(from: envelope.body))?.asProtocolError
                ?? ProtocolError(code: .internalError, message: "unknown error")
            finish(envelope.requestID, with: .failure(error))
            return
        }

        // A paged reply streams N item frames sharing our RequestID, then a
        // terminator carrying the cursor. HISTORY is the important case: the
        // gateway replays stored messages as ordinary NEW frames so the client's
        // normal ingest path handles them, and the shared RequestID is the only
        // thing distinguishing a backfilled message from live fanout.
        if let itemType = request.streamItemType, envelope.type == itemType {
            request.items.append(envelope.body)
            pending[envelope.requestID] = request
            return
        }

        finish(envelope.requestID, with: .success(
            RawReply(type: envelope.type, seq: envelope.seq, body: envelope.body, items: request.items)
        ))
    }

    private func dispatchPush(_ envelope: Envelope) {
        func decode<T: ProtoDecodable>(_ type: T.Type) -> T? {
            try? T.protoDecoded(from: envelope.body)
        }

        switch envelope.type {
        case .new:
            if let body = decode(NewMessageBody.self) { emit(.message(body)) }
        case .sendAck:
            if let body = decode(SendAckBody.self) { emit(.sendAck(body)) }
        case .readUpd:
            if let body = decode(ReadUpdateBody.self) { emit(.readReceipt(body)) }
        case .typing:
            if let body = decode(TypingBody.self) { emit(.typing(body)) }
        case .presence:
            if let body = decode(PresenceBody.self) { emit(.presence(body)) }
        case .reactUpd:
            if let body = decode(ReactUpdateBody.self) { emit(.reaction(body)) }
        case .chatInfo:
            if let body = decode(ChatInfoBody.self) { emit(.chatInfo(body)) }
        case .pinned:
            if let body = decode(PinnedBody.self) { emit(.pinned(body)) }
        case .drafts:
            if let body = decode(DraftsBody.self) { emit(.drafts(body)) }
        case .error, .authErr:
            let error = decode(ErrorBody.self)?.asProtocolError
                ?? ProtocolError(code: .internalError, message: "unknown error")
            // An *uncorrelated* auth error means the session died underneath us
            // — revoked from another device, or expired. The app has to react,
            // not just log it.
            if error.isAuthFailure {
                shouldReconnect = false
                emit(.sessionExpired(error))
            } else {
                emit(.error(error))
            }
        default:
            // Unknown types are ignored by design: a peer that does not
            // understand a type must skip it, not fail.
            break
        }
    }

    // MARK: - Requests

    struct RawReply: Sendable {
        let type: MsgType
        let seq: UInt64
        let body: Data
        let items: [Data]
    }

    private struct PendingRequest {
        let expect: MsgType
        let streamItemType: MsgType?
        var items: [Data] = []
        let continuation: CheckedContinuation<RawReply, Error>
        var timeout: Task<Void, Never>?
    }

    /// Sends a request and awaits its correlated reply.
    ///
    /// `streamItemType` turns this into a paged request: item frames of that
    /// type accumulate until a frame of `expect` terminates the page.
    @discardableResult
    func request(
        _ type: MsgType,
        body: (any ProtoEncodable)?,
        expect: MsgType,
        streamItemType: MsgType? = nil
    ) async throws -> RawReply {
        requestCounter += 1
        let requestID = requestCounter

        return try await withCheckedThrowingContinuation { continuation in
            let timeout = Task { [weak self, requestTimeout = configuration.requestTimeout] in
                try? await Task.sleep(for: requestTimeout)
                guard !Task.isCancelled else { return }
                await self?.timeOut(requestID, type: type)
            }
            pending[requestID] = PendingRequest(
                expect: expect,
                streamItemType: streamItemType,
                continuation: continuation,
                timeout: timeout
            )

            Task {
                do {
                    try await write(type, body: body, requestID: requestID)
                } catch {
                    await self.finish(requestID, with: .failure(error))
                }
            }
        }
    }

    private func timeOut(_ requestID: UInt64, type: MsgType) {
        finish(requestID, with: .failure(ClientError.requestTimedOut(type)))
    }

    private func finish(_ requestID: UInt64, with result: Result<RawReply, Error>) {
        guard let request = pending.removeValue(forKey: requestID) else { return }
        request.timeout?.cancel()
        request.continuation.resume(with: result)
    }

    /// Fire-and-forget. Used for the types the gateway answers only on failure
    /// (READ, TYPING, EDIT, DELETE) — such an error arrives uncorrelated and
    /// surfaces as an `.error` event.
    func fire(_ type: MsgType, body: (any ProtoEncodable)?) async throws {
        try await write(type, body: body, requestID: 0)
    }

    /// Assigns a sequence number and hands the frame to the single writer.
    ///
    /// The two steps are deliberately not one `await`. If each caller wrote to
    /// the socket itself, two concurrent sends could take their `Seq` here and
    /// then reach the transport in the other order — Swift actors are reentrant
    /// and make no FIFO promise across suspensions. The client would then emit
    /// a sequence that goes backwards on the wire, which is exactly what the
    /// gateway's gap detection is watching for.
    ///
    /// So the seq is assigned synchronously inside the actor and the payload is
    /// queued; one writer task drains the queue in order. This mirrors
    /// `conn.writeLoop` on the server, which is the single writer for the same
    /// reason.
    private func write(_ type: MsgType, body: (any ProtoEncodable)?, requestID: UInt64) async throws {
        guard let outbound else { throw ClientError.notConnected }
        outSeq += 1
        let envelope = Envelope(
            type: type,
            seq: outSeq,
            ack: lastServerSeq,
            requestID: requestID,
            body: body?.protoEncoded() ?? Data()
        )
        outbound.yield(envelope.encode())
    }

    private func startWriteLoop(on transport: Transport) {
        writeLoop?.cancel()
        let (stream, continuation) = AsyncStream<Data>.makeStream(bufferingPolicy: .unbounded)
        outbound = continuation
        writeLoop = Task { [weak self] in
            for await payload in stream {
                do {
                    try await transport.send(payload: payload)
                } catch {
                    // A failed write means the connection is gone. Tearing down
                    // here (rather than dropping the frame) is what turns it into
                    // a reconnect instead of a silently lost message.
                    await self?.handleDisconnect(error)
                    return
                }
            }
        }
    }

    // MARK: - Liveness & reconnect

    private func startWatchdog() {
        watchdog?.cancel()
        let interval = configuration.livenessTimeout
        watchdog = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { return }
                await self?.checkLiveness(timeout: interval)
            }
        }
    }

    private func checkLiveness(timeout: Duration) async {
        guard state == .ready else { return }
        guard lastFrameAt.duration(to: .now) > timeout else { return }
        log.notice("no frames for \(timeout.components.seconds)s; forcing a redial")
        await handleDisconnect(TransportError.connectionClosed)
    }

    private func handleDisconnect(_ error: Error) async {
        guard transport != nil else { return }  // already torn down
        await teardown(error: error)

        guard shouldReconnect else {
            state = .closed
            return
        }
        state = .reconnecting

        let delay = backoffDelay()
        reconnectAttempts += 1
        reconnectTask?.cancel()
        reconnectTask = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            await self?.redial()
        }
    }

    private func redial() async {
        guard shouldReconnect else { return }
        do {
            _ = try await dial()
        } catch let error as ProtocolError where error.isAuthFailure {
            // Retrying cannot help: the credential itself is dead.
            shouldReconnect = false
            state = .closed
            emit(.sessionExpired(error))
        } catch {
            await handleDisconnect(error)
        }
    }

    /// Exponential backoff with full jitter — the jitter is what stops a whole
    /// fleet of phones from redialling in lockstep after a gateway restart.
    private func backoffDelay() -> Duration {
        let capMs = configuration.maxBackoff.components.seconds * 1000
        let baseMs = min(Int64(1000) << min(reconnectAttempts, 20), capMs)
        let jittered = baseMs / 2 + Int64.random(in: 0...(baseMs / 2))
        return .milliseconds(jittered)
    }

    private func teardown(error: Error) async {
        readLoop?.cancel()
        readLoop = nil
        watchdog?.cancel()
        watchdog = nil
        outbound?.finish()
        outbound = nil
        writeLoop?.cancel()
        writeLoop = nil
        let transport = self.transport
        self.transport = nil
        await transport?.close()

        // Snapshot first: `finish` mutates `pending`.
        for requestID in Array(pending.keys) {
            finish(requestID, with: .failure(error))
        }
    }
}
