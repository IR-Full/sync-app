import Foundation
@testable import SynapseNetwork


enum FakeGatewayError: Error {
    case timeout
}

/// A scripted gateway.
///
/// It speaks the real protocol — real frames, real envelopes, real protobuf
/// bodies — so a test exercises the actual encoders rather than a stub of them.
/// What it fakes is only the behaviour: which reply follows which request.
actor FakeGateway: Transport {
    /// Frames the client sent us, decoded. Assert against these.
    private(set) var received: [Envelope] = []

    private var outbound: [Data] = []
    private var waiter: CheckedContinuation<Data, Error>?
    private var isClosed = false

    /// When true, AUTH is answered with an ERROR instead of AUTH_OK.
    private var rejectAuth = false
    /// When true, RESUME is refused with a (recoverable) resume-expired error.
    private var refuseResume = false
    /// The messages a HISTORY request will serve.
    private var historyMessages: [NewMessageBody] = []

    private var serverSeq: UInt64 = 0

    func setRejectAuth(_ value: Bool) { rejectAuth = value }
    func setRefuseResume(_ value: Bool) { refuseResume = value }
    func setHistory(_ messages: [NewMessageBody]) { historyMessages = messages }

    func connect() async throws {}

    func waitForEnvelope(
        ofType type: MsgType,
        timeoutAttempts: Int = 100
    ) async throws -> Envelope {
        for _ in 0..<timeoutAttempts {
            if let envelope = received.first(where: { $0.type == type }) {
                return envelope
            }

            try await Task.sleep(for: .milliseconds(10))
        }

        throw FakeGatewayError.timeout
    }

    func send(payload: Data) async throws {
        guard !isClosed else { throw TransportError.connectionClosed }
        let envelope = try Envelope.decode(payload)
        received.append(envelope)
        for reply in respond(to: envelope) {
            enqueue(reply)
        }
    }

    func receive() async throws -> Data {
        if !outbound.isEmpty {
            return outbound.removeFirst()
        }
        if isClosed { throw TransportError.connectionClosed }
        return try await withCheckedThrowingContinuation { continuation in
            waiter = continuation
        }
    }

    func close() async {
        isClosed = true
        waiter?.resume(throwing: TransportError.connectionClosed)
        waiter = nil
    }

    // MARK: - Test helpers

    /// Pushes an unsolicited frame, the way live fanout would.
    func push(_ type: MsgType, body: (any ProtoEncodable)?) {
        enqueue(Envelope(type: type, requestID: 0, body: body?.protoEncoded() ?? Data()))
    }

    func envelopes(ofType type: MsgType) -> [Envelope] {
        received.filter { $0.type == type }
    }

    // MARK: - Internals

    private func enqueue(_ envelope: Envelope) {
        serverSeq += 1
        var stamped = envelope
        stamped.seq = serverSeq
        let payload = stamped.encode()
        if let waiter {
            self.waiter = nil
            waiter.resume(returning: payload)
        } else {
            outbound.append(payload)
        }
    }

    private func respond(to envelope: Envelope) -> [Envelope] {
        switch envelope.type {
        case .hello:
            var welcome = WelcomeBody()
            welcome.serverVersion = "synapse/test"
            welcome.sessionID = "session-1"
            // The server agrees to the intersection; the client asked for
            // resume + typing.
            welcome.caps = [.resume, .typingSignals]
            welcome.heartbeatMs = 20_000
            welcome.maxInflight = 256
            welcome.resumeSupported = true
            return [reply(.welcome, to: envelope, body: welcome)]

        case .auth:
            guard !rejectAuth else {
                var error = ErrorBody()
                error.code = .unauthenticated
                error.message = "authentication failed"
                return [reply(.error, to: envelope, body: error)]
            }
            var ok = AuthOKBody()
            ok.userID = "user-1"
            ok.deviceID = "device-1"
            ok.sessionID = "session-1"
            ok.token = "token-1"
            ok.resumeToken = "resume-1"
            return [reply(.authOK, to: envelope, body: ok)]

        case .resume:
            guard !refuseResume else {
                var error = ErrorBody()
                error.code = .resumeExpired
                error.message = "resume rejected; re-authenticate"
                return [reply(.error, to: envelope, body: error)]
            }
            var ok = ResumeOKBody()
            ok.sessionID = "session-1"
            return [reply(.resumeOK, to: envelope, body: ok)]

        case .ping:
            return [reply(.pong, to: envelope, body: nil)]

        case .send:
            let sent = (try? SendBody.protoDecoded(from: envelope.body)) ?? SendBody()
            var ack = SendAckBody()
            ack.dedupKey = sent.dedupKey
            ack.messageID = "msg-\(received.count)"
            ack.chatID = sent.chatID.hasPrefix("@") ? "555" : sent.chatID
            ack.chatSeq = UInt64(received.count)
            ack.timestamp = 1_700_000_000_000
            return [reply(.sendAck, to: envelope, body: ack)]

        case .history:
            // The real shape: N item frames sharing our RequestID, then the
            // terminator carrying the cursor.
            var page = historyMessages.map { reply(.new, to: envelope, body: $0) }
            var done = HistoryOKBody()
            done.chatID = (try? HistoryBody.protoDecoded(from: envelope.body).chatID) ?? ""
            done.nextBefore = historyMessages.last?.chatSeq ?? 0
            done.done = true
            page.append(reply(.historyOK, to: envelope, body: done))
            return page

        case .pinList:
            var pinned = PinnedBody()
            pinned.chatID = "555"  // the resolved snowflake, not the @handle
            return [reply(.pinned, to: envelope, body: pinned)]

        case .pushToken:
            return [reply(.pushToken, to: envelope, body: PushTokenBody())]

        default:
            return []
        }
    }

    private func reply(_ type: MsgType, to request: Envelope, body: (any ProtoEncodable)?) -> Envelope {
        Envelope(type: type, requestID: request.requestID, body: body?.protoEncoded() ?? Data())
    }
}
