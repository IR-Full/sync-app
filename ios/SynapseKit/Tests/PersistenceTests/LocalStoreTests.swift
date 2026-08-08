import XCTest
@testable import SynapseDomain
@testable import SynapsePersistence

/// Cache tests. The interesting cases are the ones where the offline story could
/// silently break: the dedup key that makes a retry safe, the optimistic row
/// that must become the acked row rather than a twin, and the unread count that
/// is derived rather than stored.
///
/// Note the hoisted `let`s before every assertion — `XCTAssert*` takes
/// autoclosures, which may throw but may not `await`.
final class LocalStoreTests: XCTestCase {
    private var databaseURL: URL!
    private var store: LocalStore!

    override func setUp() async throws {
        databaseURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("synapse-test-\(UUID().uuidString).sqlite")
        let database = try Database(url: databaseURL)
        try await database.prepare()
        store = LocalStore(database: database, broker: ChangeBroker())
        try await store.setMeta(LocalStore.MetaKey.userID, "me")
    }

    override func tearDown() async throws {
        store = nil
        try? FileManager.default.removeItem(at: databaseURL)
    }

    private func makeChat(id: String = "10", lastReadSeq: UInt64 = 0) async throws {
        try await store.upsertChat(
            Chat(id: id, kind: .direct, title: "Bob", lastReadSeq: lastReadSeq)
        )
    }

    // MARK: - Messages

    func testMessagesComeBackOldestFirst() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "c", chatID: "10", senderID: "bob", seq: 3, text: "three"),
            Message(id: "a", chatID: "10", senderID: "bob", seq: 1, text: "one"),
            Message(id: "b", chatID: "10", senderID: "bob", seq: 2, text: "two"),
        ])

        let texts = try await store.messages(chatID: "10").map(\.text)
        XCTAssertEqual(texts, ["one", "two", "three"])
    }

    /// Ordering is by `seq`, never by timestamp: `seq` is the server's gap-free
    /// per-chat position, and two messages can share a millisecond.
    func testOrderingUsesSeqNotTimestamp() async throws {
        try await makeChat()
        let sameInstant = Date(timeIntervalSince1970: 1_700_000_000)
        try await store.upsertMessages([
            Message(id: "b", chatID: "10", senderID: "bob", seq: 2, text: "second", sentAt: sameInstant),
            Message(id: "a", chatID: "10", senderID: "bob", seq: 1, text: "first", sentAt: sameInstant),
        ])

        let texts = try await store.messages(chatID: "10").map(\.text)
        XCTAssertEqual(texts, ["first", "second"])
    }

    func testUpsertIsIdempotentOnRedelivery() async throws {
        try await makeChat()
        let message = Message(id: "a", chatID: "10", senderID: "bob", seq: 1, text: "hi")
        try await store.upsertMessages([message])
        try await store.upsertMessages([message])  // a resume replay, say

        let count = try await store.messages(chatID: "10").count
        XCTAssertEqual(count, 1)
    }

    /// A replayed NEW must not undo a read receipt we already applied.
    func testRedeliveryDoesNotDowngradeReadState() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "a", chatID: "10", senderID: "me", seq: 1, text: "hi", state: .sent)
        ])
        try await store.applyReadReceipt(chatID: "10", upToSeq: 1, ourUserID: "me")

        let afterReceipt = try await store.message(id: "a")?.state
        XCTAssertEqual(afterReceipt, .read)

        try await store.upsertMessages([
            Message(id: "a", chatID: "10", senderID: "me", seq: 1, text: "hi", state: .sent)
        ])

        let afterReplay = try await store.message(id: "a")?.state
        XCTAssertEqual(afterReplay, .read)
    }

    // MARK: - Optimistic send

    /// The optimistic row is keyed by the dedup key and is *promoted* to the
    /// server id — not deleted and reinserted — so the message keeps its
    /// identity in the list and does not animate out and back in.
    func testAckPromotesTheOptimisticRowInPlace() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "dedup-1", chatID: "10", senderID: "me", seq: 0,
                    text: "hello", state: .sending, dedupKey: "dedup-1")
        ])
        try await store.enqueue(.init(dedupKey: "dedup-1", chatID: "10", text: "hello"))

        try await store.confirmMessage(
            dedupKey: "dedup-1", messageID: "900", chatID: "10", seq: 7, timestamp: Date()
        )

        let messages = try await store.messages(chatID: "10")
        XCTAssertEqual(messages.count, 1, "promotion must not leave the optimistic row behind")
        XCTAssertEqual(messages.first?.id, "900")
        XCTAssertEqual(messages.first?.seq, 7)
        XCTAssertEqual(messages.first?.state, .sent)

        let outbox = try await store.pendingOutbox()
        XCTAssertTrue(outbox.isEmpty, "a confirmed send leaves the queue")
    }

    /// The whole point of the dedup key: a retry whose first attempt actually
    /// landed resolves to the same message instead of posting a duplicate.
    func testDuplicateAckDoesNotCreateASecondMessage() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "dedup-1", chatID: "10", senderID: "me",
                    text: "hello", state: .sending, dedupKey: "dedup-1")
        ])
        try await store.confirmMessage(
            dedupKey: "dedup-1", messageID: "900", chatID: "10", seq: 7, timestamp: Date()
        )
        // The retry's ack comes back with `duplicate = true` and the same id.
        try await store.confirmMessage(
            dedupKey: "dedup-1", messageID: "900", chatID: "10", seq: 7, timestamp: Date()
        )

        let count = try await store.messages(chatID: "10").count
        XCTAssertEqual(count, 1)
    }

    func testOutboxIsFlushedOldestFirst() async throws {
        try await makeChat()
        let base = Date(timeIntervalSince1970: 1_700_000_000)
        try await store.enqueue(.init(dedupKey: "b", chatID: "10", text: "second",
                                      createdAt: base.addingTimeInterval(1)))
        try await store.enqueue(.init(dedupKey: "a", chatID: "10", text: "first", createdAt: base))

        let queued = try await store.pendingOutbox().map(\.text)
        XCTAssertEqual(queued, ["first", "second"],
                       "messages must leave in the order they were typed")
    }

    func testEnqueueIsIdempotent() async throws {
        try await makeChat()
        try await store.enqueue(.init(dedupKey: "a", chatID: "10", text: "hi"))
        try await store.enqueue(.init(dedupKey: "a", chatID: "10", text: "hi"))

        let count = try await store.pendingOutbox().count
        XCTAssertEqual(count, 1)
    }

    // MARK: - Unread & receipts

    /// Unread is derived from `seq > last_read_seq`, not counted into a column,
    /// so a receipt from another device fixes the badge with nothing to drift.
    func testUnreadCountIsDerivedAndExcludesOurOwnMessages() async throws {
        try await makeChat(lastReadSeq: 1)
        try await store.upsertMessages([
            Message(id: "a", chatID: "10", senderID: "bob", seq: 1, text: "read"),
            Message(id: "b", chatID: "10", senderID: "bob", seq: 2, text: "unread"),
            Message(id: "c", chatID: "10", senderID: "bob", seq: 3, text: "unread too"),
            Message(id: "d", chatID: "10", senderID: "me", seq: 4, text: "mine"),
        ])

        let unread = try await store.chatSummaries().first?.unreadCount
        XCTAssertEqual(unread, 2)
    }

    func testDeletedMessagesDoNotCountAsUnread() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "a", chatID: "10", senderID: "bob", seq: 1, text: "", isDeleted: true),
            Message(id: "b", chatID: "10", senderID: "bob", seq: 2, text: "real"),
        ])

        let unread = try await store.chatSummaries().first?.unreadCount
        XCTAssertEqual(unread, 1)
    }

    func testReadMarkerNeverMovesBackwards() async throws {
        try await makeChat()
        try await store.markRead(chatID: "10", upToSeq: 10)
        try await store.markRead(chatID: "10", upToSeq: 4)

        let marker = try await store.chat(id: "10")?.lastReadSeq
        XCTAssertEqual(marker, 10)
    }

    func testReceiptMarksOnlyOurOwnMessagesRead() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "a", chatID: "10", senderID: "me", seq: 1, text: "mine", state: .sent),
            Message(id: "b", chatID: "10", senderID: "bob", seq: 2, text: "theirs", state: .sent),
        ])
        try await store.applyReadReceipt(chatID: "10", upToSeq: 2, ourUserID: "me")

        let ours = try await store.message(id: "a")?.state
        let theirs = try await store.message(id: "b")?.state
        XCTAssertEqual(ours, .read)
        XCTAssertEqual(theirs, .sent, "a receipt says what the peer read, not what we did")
    }

    // MARK: - Paging & expiry

    /// The paging cursor must ignore queued messages, whose `seq` is 0 — reading
    /// 0 as "the oldest we hold" would ask the server for the newest page
    /// forever and history would never load.
    func testOldestSeqIgnoresUnsentMessages() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "queued", chatID: "10", senderID: "me", seq: 0,
                    text: "not sent yet", state: .sending, dedupKey: "queued"),
            Message(id: "a", chatID: "10", senderID: "bob", seq: 5, text: "old"),
            Message(id: "b", chatID: "10", senderID: "bob", seq: 9, text: "new"),
        ])

        let cursor = try await store.oldestSeq(chatID: "10")
        XCTAssertEqual(cursor, 5)
    }

    /// The self-destruct deadline travels with the message so a client that was
    /// offline when it passed still stops showing it.
    func testExpiredMessagesArePurgedLocally() async throws {
        try await makeChat()
        let now = Date()
        try await store.upsertMessages([
            Message(id: "gone", chatID: "10", senderID: "bob", seq: 1, text: "burn",
                    expiresAt: now.addingTimeInterval(-60)),
            Message(id: "stays", chatID: "10", senderID: "bob", seq: 2, text: "keep",
                    expiresAt: now.addingTimeInterval(600)),
            Message(id: "forever", chatID: "10", senderID: "bob", seq: 3, text: "normal"),
        ])

        try await store.purgeExpired(now: now)

        let ids = try await store.messages(chatID: "10").map(\.id)
        XCTAssertEqual(ids, ["stays", "forever"])
    }

    // MARK: - Attachments & drafts

    /// The upload happened before the row was queued, so what waits in the
    /// outbox is the message carrying the `media_ref` — and it has to survive a
    /// restart intact, or the recipient gets a caption with no photo.
    func testOutboxRoundTripsAnAttachment() async throws {
        try await makeChat()
        let photo = Attachment(
            kind: .image, mediaRef: "m900-abc", filename: "photo.jpg",
            mime: "image/jpeg", size: 2048, width: 800, height: 600
        )
        try await store.enqueue(
            .init(dedupKey: "k", chatID: "10", text: "look", attachment: photo)
        )

        let entry = try await store.outboxEntry(dedupKey: "k")
        XCTAssertEqual(entry?.attachment, photo)
    }

    func testDraftIsStoredAndClearedByEmptyText() async throws {
        try await makeChat()
        try await store.upsertDraft(chatID: "10", text: "half a thought", replyTo: nil, updatedAt: Date())

        let saved = try await store.draft(chatID: "10")
        XCTAssertEqual(saved, "half a thought")

        try await store.upsertDraft(chatID: "10", text: "", replyTo: nil, updatedAt: Date())
        let cleared = try await store.draft(chatID: "10")
        XCTAssertEqual(cleared, "", "empty text clears the draft, it does not store an empty one")
    }

    /// Two devices composing at once should converge on the later keystroke, not
    /// on whichever mirrored frame happened to arrive second.
    func testStaleDraftDoesNotOverwriteANewerOne() async throws {
        try await makeChat()
        let now = Date()
        try await store.upsertDraft(chatID: "10", text: "newer", replyTo: nil, updatedAt: now)
        try await store.upsertDraft(
            chatID: "10", text: "older", replyTo: nil, updatedAt: now.addingTimeInterval(-30)
        )

        let text = try await store.draft(chatID: "10")
        XCTAssertEqual(text, "newer")
    }

    // MARK: - Chats

    func testChatUpsertDoesNotBlankFieldsAPartialWriteOmits() async throws {
        try await store.upsertChat(
            Chat(id: "10", kind: .group, title: "Team", ownerID: "me", peerUserID: nil)
        )
        // A later, thinner write (an implied chat from an inbound message).
        try await store.upsertChat(Chat(id: "10", kind: .group, title: ""))

        let chat = try await store.chat(id: "10")
        XCTAssertEqual(chat?.title, "Team", "a blank title must not erase a known one")
        XCTAssertEqual(chat?.ownerID, "me")
    }

    func testHiddenChatsLeaveTheList() async throws {
        try await makeChat()
        let before = try await store.chatSummaries().count
        XCTAssertEqual(before, 1)

        try await store.setChatHidden("10", hidden: true)

        let after = try await store.chatSummaries()
        XCTAssertTrue(after.isEmpty)
    }

    func testWipeRemovesEverything() async throws {
        try await makeChat()
        try await store.upsertMessages([
            Message(id: "a", chatID: "10", senderID: "bob", seq: 1, text: "hi")
        ])
        try await store.enqueue(.init(dedupKey: "k", chatID: "10", text: "queued"))
        try await store.upsertDraft(chatID: "10", text: "unsent", replyTo: nil, updatedAt: Date())

        try await store.wipe()

        let draft = try await store.draft(chatID: "10")
        XCTAssertEqual(draft, "", "a draft is content too — logout must not leave it behind")

        let chats = try await store.chatSummaries()
        let messages = try await store.messages(chatID: "10")
        let outbox = try await store.pendingOutbox()
        XCTAssertTrue(chats.isEmpty)
        XCTAssertTrue(messages.isEmpty)
        XCTAssertTrue(outbox.isEmpty)
    }
}
