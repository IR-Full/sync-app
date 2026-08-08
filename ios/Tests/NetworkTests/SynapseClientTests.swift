import XCTest
@testable import SynapseNetwork

/// End-to-end tests of the client against a scripted gateway.
///
/// These are the tests that would have caught the mistakes that are easy to make
/// when reimplementing a protocol from its server: sending AUTH before HELLO,
/// treating a streamed history page as a single reply, letting the resume cursor
/// leak across a fresh login, or confusing a backfilled message with live fanout.
final class SynapseClientTests: XCTestCase {

    private func makeClient(_ gateway: FakeGateway) -> SynapseClient {
        SynapseClient(
            environment: .testing,
            configuration: .init(requestTimeout: .seconds(3)),
            transportFactory: { gateway }
        )
    }

    // MARK: - Handshake

    func testHandshakeOrderIsHelloThenAuth() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)

        let session = try await client.connect(
            credentials: .password(username: "alice", password: "secret123", register: false)
        )

        let types = await gateway.received.map(\.type)
        XCTAssertEqual(Array(types.prefix(2)), [.hello, .auth],
                       "the gateway reads HELLO first and rejects anything else")
        XCTAssertEqual(session.userID, "user-1")
        XCTAssertEqual(session.resumeToken, "resume-1")
    }

    func testHelloAdvertisesResumeButNotCompression() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        let hellos = await gateway.envelopes(ofType: .hello)
        let hello = try XCTUnwrap(hellos.first)
        let body = try HelloBody.protoDecoded(from: hello.body)
        XCTAssertTrue(body.caps.contains(.resume))
        XCTAssertTrue(body.caps.contains(.typingSignals))
        // We cannot decompress, so we must not claim we can — otherwise the
        // gateway will happily gzip everything it sends us.
        XCTAssertFalse(body.caps.contains(.compression))
        XCTAssertFalse(body.caps.contains(.zstd))
        XCTAssertEqual(body.platform, "ios")
    }

    func testFirstEnvelopeSequenceStartsAtOne() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        let seqs = await gateway.received.map(\.seq)
        XCTAssertEqual(seqs.first, 1, "our per-connection sequence is 1-based")
        XCTAssertEqual(seqs, Array(1...UInt64(seqs.count)), "and strictly monotonic")
    }

    func testFailedAuthSurfacesAsAnAuthError() async {
        let gateway = FakeGateway()
        await gateway.setRejectAuth(true)
        let client = makeClient(gateway)

        do {
            _ = try await client.connect(
                credentials: .password(username: "alice", password: "wrong", register: false)
            )
            XCTFail("expected authentication to fail")
        } catch let error as ProtocolError {
            XCTAssertTrue(error.isAuthFailure)
            XCTAssertEqual(error.code, .unauthenticated)
        } catch {
            XCTFail("expected a ProtocolError, got \(error)")
        }
    }

    // MARK: - Request correlation

    func testRepliesCorrelateByRequestID() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        // Two sends in flight at once: correlation, not ordering, is what pairs
        // each ack with its request.
        async let first = client.sendMessage(chatID: "10", dedupKey: "key-a", text: "one")
        async let second = client.sendMessage(chatID: "10", dedupKey: "key-b", text: "two")
        let acks = try await [first, second]

        XCTAssertEqual(Set(acks.map(\.dedupKey)), ["key-a", "key-b"])
    }

    /// The gateway streams a history page as ordinary NEW frames sharing our
    /// request id, then terminates it with HISTORY_OK. Getting this wrong means
    /// either resolving on the first message or hanging forever.
    func testHistoryCollectsStreamedPageUntilTheTerminator() async throws {
        let gateway = FakeGateway()
        await gateway.setHistory((1...3).map { index in
            var message = NewMessageBody()
            message.messageID = "m\(index)"
            message.chatID = "10"
            message.senderID = "user-2"
            message.chatSeq = UInt64(index)
            message.text = "message \(index)"
            return message
        })
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        let page = try await client.history(chatID: "10", beforeSeq: 0, limit: 50)
        XCTAssertEqual(page.messages.count, 3)
        XCTAssertEqual(page.messages.map(\.messageID), ["m1", "m2", "m3"])
        XCTAssertTrue(page.page.done)
    }

    /// A backfilled message must not also arrive as a push — the shared request
    /// id is the only thing distinguishing it from live fanout, and double
    /// ingest would duplicate every history page in the UI.
    func testStreamedHistoryDoesNotAlsoEmitPushEvents() async throws {
        let gateway = FakeGateway()
        var backfilled = NewMessageBody()
        backfilled.messageID = "m1"
        backfilled.chatID = "10"
        backfilled.chatSeq = 1
        await gateway.setHistory([backfilled])

        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        let collector = EventCollector()
        let events = await client.events()
        let pump = Task { for await event in events { await collector.record(event) } }
        defer { pump.cancel() }

        _ = try await client.history(chatID: "10")
        try await Task.sleep(for: .milliseconds(120))

        let messageEvents = await collector.messageCount
        XCTAssertEqual(messageEvents, 0, "history items belong to the reply, not the push stream")
    }

    func testLiveFanoutIsEmittedAsAPushEvent() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        let collector = EventCollector()
        let events = await client.events()
        let pump = Task { for await event in events { await collector.record(event) } }
        defer { pump.cancel() }

        var incoming = NewMessageBody()
        incoming.messageID = "m99"
        incoming.chatID = "10"
        incoming.senderID = "user-2"
        incoming.chatSeq = 9
        incoming.text = "hello"
        await gateway.push(.new, body: incoming)

        try await Task.sleep(for: .milliseconds(150))
        let count = await collector.messageCount
        XCTAssertEqual(count, 1)
        let text = await collector.lastMessageText
        XCTAssertEqual(text, "hello")
    }

    /// The gateway pings on its heartbeat; not answering gets the connection
    /// reaped at 60s of silence, and the PONG doubles as the activity that keeps
    /// our presence TTL fresh.
    func testPingIsAnsweredWithPong() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        await gateway.push(.ping, body: nil)
        try await Task.sleep(for: .milliseconds(150))

        let pongCount = await gateway.envelopes(ofType: .pong).count
        XCTAssertEqual(pongCount, 1)
    }

    /// The `@handle` → chat-id resolve rides PIN_LIST, whose reply carries the
    /// *resolved* snowflake. HISTORY_OK would echo the handle we sent, which is
    /// exactly the trap this protects against.
    func testResolveDirectChatReturnsTheSnowflakeNotTheHandle() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        let chatID = try await client.resolveDirectChat(username: "bob")
        XCTAssertEqual(chatID, "555")

        let pinRequests = await gateway.envelopes(ofType: .pinList)
        let request = try XCTUnwrap(pinRequests.first)
        let body = try PinActionBody.protoDecoded(from: request.body)
        XCTAssertEqual(body.chatID, "@bob", "the handle is sent as-is; the gateway resolves it")
    }

    func testAckPiggybacksTheHighestServerSequence() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        await gateway.push(.presence, body: PresenceBody())
        try await Task.sleep(for: .milliseconds(120))
        _ = try await client.sendMessage(chatID: "10", dedupKey: "k", text: "hi")

        let sends = await gateway.envelopes(ofType: .send)
        let send = try XCTUnwrap(sends.first)
        XCTAssertGreaterThan(send.ack, 0, "the ack field carries the highest server seq we processed")
    }

    // MARK: - Fire-and-forget

    /// READ, TYPING, EDIT and DELETE are answered only on failure, so they must
    /// not occupy a request slot waiting for a reply that will never come.
    func testFireAndForgetSendsRequestIDZero() async throws {
        let gateway = FakeGateway()
        let client = makeClient(gateway)
        _ = try await client.connect(credentials: .token("t"))

        try await client.setTyping(chatID: "10", active: true)
        try await client.markRead(chatID: "10", upToMessageID: "m1", upToChatSeq: 4)

        let typingFrames = await gateway.envelopes(ofType: .typing)
        let readFrames = await gateway.envelopes(ofType: .read)
        let typing = try XCTUnwrap(typingFrames.first)
        let read = try XCTUnwrap(readFrames.first)
        XCTAssertEqual(typing.requestID, 0)
        XCTAssertEqual(read.requestID, 0)
    }
}

/// Collects pushes off the client's event stream.
private actor EventCollector {
    private(set) var messageCount = 0
    private(set) var lastMessageText: String?

    func record(_ event: SynapseClient.Event) {
        if case .message(let body) = event {
            messageCount += 1
            lastMessageText = body.text
        }
    }
}

extension ServerEnvironment {
    /// A placeholder environment — the transport is injected, so none of these
    /// values are dialled.
    static var testing: ServerEnvironment {
        ServerEnvironment(
            name: .dev,
            gatewayURL: URL(string: "ws://localhost:8080/ws")!,
            tcpHost: "localhost",
            tcpPort: 7000,
            transport: .webSocket,
            mediaBaseURL: nil,
            allowsInsecureTLS: true
        )
    }
}
