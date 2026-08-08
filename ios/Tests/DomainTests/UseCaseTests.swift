import XCTest
@testable import SynapseDomain

/// Use-case tests. The rules worth testing are the ones that exist because the
/// *server* behaves a certain way — those are the ones a future edit is most
/// likely to "simplify" away.
final class SendMessageUseCaseTests: XCTestCase {

    func testTrimsAndForwards() async throws {
        let repository = SpyMessageRepository()
        let send = SendMessageUseCase(messages: repository)

        try await send(chatID: "10", text: "  hello  ")

        let sent = await repository.sent
        XCTAssertEqual(sent.count, 1)
        XCTAssertEqual(sent.first?.text, "hello")
    }

    func testRejectsWhitespaceOnlyText() async {
        let repository = SpyMessageRepository()
        let send = SendMessageUseCase(messages: repository)

        do {
            try await send(chatID: "10", text: "   \n ")
            XCTFail("expected rejection")
        } catch {
            XCTAssertEqual(error as? ValidationError, .emptyMessage)
        }
        let sent = await repository.sent
        XCTAssertTrue(sent.isEmpty)
    }

    /// `message.MaxTextLen` is a *byte* limit, and the server counts UTF-8. A
    /// character-count check would let 8192 Cyrillic characters (≈16 KB)
    /// through, and the send would be refused only after a round trip.
    func testLimitIsCountedInBytesNotCharacters() async {
        let repository = SpyMessageRepository()
        let send = SendMessageUseCase(messages: repository)

        // 4200 two-byte characters = 8400 bytes: under the character limit,
        // over the byte limit.
        let cyrillic = String(repeating: "я", count: 4200)
        XCTAssertLessThan(cyrillic.count, ServerLimits.maxMessageBytes)
        XCTAssertGreaterThan(cyrillic.utf8.count, ServerLimits.maxMessageBytes)

        do {
            try await send(chatID: "10", text: cyrillic)
            XCTFail("expected rejection")
        } catch {
            guard case .messageTooLong(let bytes, let limit)? = error as? ValidationError else {
                return XCTFail("wrong error: \(error)")
            }
            XCTAssertEqual(bytes, 8400)
            XCTAssertEqual(limit, ServerLimits.maxMessageBytes)
        }
    }

    /// A photo on its own is a message. Only one with neither text nor
    /// attachment is empty — the server's own `SendBody` allows either.
    func testAttachmentWithoutTextIsAllowed() async throws {
        let repository = SpyMessageRepository()
        let send = SendMessageUseCase(messages: repository)
        let photo = Attachment(kind: .image, mediaRef: "m123-abc")

        try await send(chatID: "10", text: "", attachment: photo)

        let sent = await repository.sent
        XCTAssertEqual(sent.count, 1)
        XCTAssertEqual(sent.first?.attachment?.mediaRef, "m123-abc")
        XCTAssertEqual(sent.first?.text, "")
    }

    /// The caption still has to fit, attachment or not.
    func testAttachmentDoesNotExemptTheCaptionFromTheLimit() async {
        let repository = SpyMessageRepository()
        let send = SendMessageUseCase(messages: repository)
        do {
            try await send(
                chatID: "10",
                text: String(repeating: "a", count: ServerLimits.maxMessageBytes + 1),
                attachment: Attachment(kind: .image, mediaRef: "m1")
            )
            XCTFail("expected rejection")
        } catch {
            guard case .messageTooLong? = error as? ValidationError else {
                return XCTFail("wrong error: \(error)")
            }
        }
    }

    func testAcceptsExactlyTheLimit() async throws {
        let repository = SpyMessageRepository()
        let send = SendMessageUseCase(messages: repository)
        try await send(chatID: "10", text: String(repeating: "a", count: ServerLimits.maxMessageBytes))
        let sent = await repository.sent
        XCTAssertEqual(sent.count, 1)
    }
}

final class HandleNormalizationTests: XCTestCase {

    /// The gateway lowercases the handle on its side, so `@Bob`, `bob` and
    /// `@bob` must all reach the same chat rather than three.
    func testAllTheSpellingsPeopleActuallyType() {
        XCTAssertEqual(OpenDirectChatUseCase.normalize("bob"), "bob")
        XCTAssertEqual(OpenDirectChatUseCase.normalize("@bob"), "bob")
        XCTAssertEqual(OpenDirectChatUseCase.normalize("@Bob"), "bob")
        XCTAssertEqual(OpenDirectChatUseCase.normalize("  @BOB  "), "bob")
        XCTAssertEqual(OpenDirectChatUseCase.normalize("@@bob"), "bob")
    }

    func testEmptyHandleIsRejectedBeforeARoundTrip() async {
        let repository = SpyChatRepository()
        let open = OpenDirectChatUseCase(chats: repository)
        do {
            _ = try await open(handle: "@@@")
            XCTFail("expected rejection")
        } catch {
            XCTAssertEqual(error as? ValidationError, .usernameInvalid)
        }
    }
}

final class CreateGroupUseCaseTests: XCTestCase {

    /// Members are passed as handles, not pre-resolved ids. Resolving them here
    /// would cost N round trips *and* create N accidental 1:1 chats, because the
    /// only resolve the protocol offers also ensures a direct chat exists.
    func testPassesHandlesThroughForTheGatewayToResolve() async throws {
        let repository = SpyChatRepository()
        let create = CreateGroupUseCase(chats: repository)

        _ = try await create(title: "  Team  ", memberHandles: ["@Alice", "bob"], isChannel: false)

        let created = await repository.createdGroups
        XCTAssertEqual(created.first?.title, "Team")
        XCTAssertEqual(created.first?.members, ["@alice", "@bob"])
        XCTAssertEqual(created.first?.isChannel, false)
    }

    func testRejectsEmptyTitle() async {
        let create = CreateGroupUseCase(chats: SpyChatRepository())
        do {
            _ = try await create(title: "   ", memberHandles: [], isChannel: false)
            XCTFail("expected rejection")
        } catch {
            XCTAssertEqual(error as? ValidationError, .emptyTitle)
        }
    }

    func testRejectsTitleLongerThanTheServerAccepts() async {
        let create = CreateGroupUseCase(chats: SpyChatRepository())
        do {
            _ = try await create(
                title: String(repeating: "x", count: ServerLimits.maxChatTitleLength + 1),
                memberHandles: [], isChannel: false
            )
            XCTFail("expected rejection")
        } catch {
            XCTAssertEqual(
                error as? ValidationError,
                .titleTooLong(limit: ServerLimits.maxChatTitleLength)
            )
        }
    }
}

final class MarkChatReadUseCaseTests: XCTestCase {

    /// A read marker is a high-water mark shared across the account's devices.
    /// Sending a lower one would move it backwards everywhere.
    func testNeverMovesTheMarkerBackwards() async {
        let repository = SpyMessageRepository()
        let markRead = MarkChatReadUseCase(messages: repository)
        let chat = Chat(id: "10", kind: .direct, title: "", lastReadSeq: 20)

        await markRead(chat: chat, upToSeq: 15)
        var marks = await repository.readMarks
        XCTAssertTrue(marks.isEmpty)

        await markRead(chat: chat, upToSeq: 21)
        marks = await repository.readMarks
        XCTAssertEqual(marks, [21])
    }
}

// MARK: - Spies

private actor SpyMessageRepository: MessageRepository {
    struct Sent: Equatable {
        let chatID: String
        let text: String
        let replyTo: String?
        let attachment: Attachment?
    }

    private(set) var sent: [Sent] = []
    private(set) var readMarks: [UInt64] = []
    private(set) var drafts: [String] = []

    nonisolated func observeMessages(chatID: String) -> AsyncStream<[Message]> {
        AsyncStream { $0.finish() }
    }
    nonisolated func observeTyping(chatID: String) -> AsyncStream<Set<String>> {
        AsyncStream { $0.finish() }
    }
    nonisolated func observeDraft(chatID: String) -> AsyncStream<String> {
        AsyncStream { $0.finish() }
    }

    func refreshLatest(chatID: String) async throws {}
    func loadOlder(chatID: String) async throws -> Bool { false }
    func send(chatID: String, text: String, replyTo: String?, attachment: Attachment?) async throws {
        sent.append(Sent(chatID: chatID, text: text, replyTo: replyTo, attachment: attachment))
    }
    func saveDraft(chatID: String, text: String, replyTo: String?) async { drafts.append(text) }
    func retry(messageID: String) async throws {}
    func edit(chatID: String, messageID: String, text: String) async throws {}
    func delete(chatID: String, messageID: String, forEveryone: Bool) async throws {}
    func toggleReaction(chatID: String, messageID: String, emoji: String) async throws {}
    func markRead(chatID: String, upToSeq: UInt64) async { readMarks.append(upToSeq) }
    func setTyping(chatID: String, active: Bool) async {}
}

private actor SpyChatRepository: ChatRepository {
    struct CreatedGroup: Equatable {
        let title: String
        let members: [String]
        let isChannel: Bool
    }

    private(set) var createdGroups: [CreatedGroup] = []
    private(set) var openedHandles: [String] = []

    nonisolated func observeChats() -> AsyncStream<[ChatSummary]> {
        AsyncStream { $0.finish() }
    }

    func chat(id: String) async -> Chat? { nil }

    func openDirectChat(username: String) async throws -> Chat {
        openedHandles.append(username)
        return Chat(id: "555", kind: .direct, title: "@" + username)
    }

    func createGroup(title: String, memberHandles: [String], isChannel: Bool) async throws -> Chat {
        createdGroups.append(CreatedGroup(title: title, members: memberHandles, isChannel: isChannel))
        return Chat(id: "777", kind: isChannel ? .channel : .group, title: title)
    }

    func join(code: String?, handle: String?) async throws -> String { "888" }
    func setMuted(chatID: String, muted: Bool) async {}
    func hideLocally(chatID: String) async {}
}
