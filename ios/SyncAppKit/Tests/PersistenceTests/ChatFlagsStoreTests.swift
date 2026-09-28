import XCTest
@testable import SyncAppCrypto
@testable import SyncAppDomain
@testable import SyncAppPersistence

/// The v3 schema: per-member chat flags, activity ordering, ratchet sessions and the
/// pending-ack table.
///
/// The ordering tests matter more than they look. The client merges pages as the server
/// sends them, so a local sort that differs from the server's reshuffles rows on every
/// sync — visible to the user as a list that will not sit still.
final class ChatFlagsStoreTests: XCTestCase {
    private var databaseURL: URL!
    private var store: LocalStore!

    override func setUp() async throws {
        databaseURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("syncapp-flags-\(UUID().uuidString).sqlite")
        let database = try Database(url: databaseURL)
        try await database.prepare()
        store = LocalStore(database: database, broker: ChangeBroker())
        try await store.setMeta(LocalStore.MetaKey.userID, "me")
    }

    override func tearDown() async throws {
        store = nil
        try? FileManager.default.removeItem(at: databaseURL)
    }

    private func chat(
        _ id: String,
        kind: Chat.Kind = .direct,
        activity: Date? = nil,
        pinned: Bool = false,
        archived: Bool = false,
        mutedUntil: Date? = nil
    ) -> Chat {
        Chat(
            id: id,
            kind: kind,
            title: "chat \(id)",
            mutedUntil: mutedUntil,
            isPinned: pinned,
            isArchived: archived,
            lastActivityAt: activity
        )
    }

    // MARK: - Ordering

    func testListIsOrderedByActivityNotByID() async throws {
        let base = Date(timeIntervalSince1970: 1_700_000_000)
        try await store.upsertChat(chat("1", activity: base))
        try await store.upsertChat(chat("2", activity: base.addingTimeInterval(60)))
        try await store.upsertChat(chat("3", activity: base.addingTimeInterval(30)))

        let ids = try await store.chatSummaries().map(\.chat.id)
        XCTAssertEqual(ids, ["2", "3", "1"])
    }

    func testPinnedChatsSortAboveEverythingElse() async throws {
        let base = Date(timeIntervalSince1970: 1_700_000_000)
        try await store.upsertChat(chat("busy", activity: base.addingTimeInterval(3600)))
        try await store.upsertChat(chat("quiet", activity: base, pinned: true))

        // Pinning means "above everything", not "above things with less traffic".
        let ids = try await store.chatSummaries().map(\.chat.id)
        XCTAssertEqual(ids, ["quiet", "busy"])
    }

    func testArchivedChatsLeaveTheMainListAndAppearInTheArchive() async throws {
        try await store.upsertChat(chat("kept"))
        try await store.upsertChat(chat("filed", archived: true))

        let main = try await store.chatSummaries().map(\.chat.id)
        let archive = try await store.chatSummaries(archived: true).map(\.chat.id)
        XCTAssertEqual(main, ["kept"])
        XCTAssertEqual(archive, ["filed"])
    }

    // MARK: - Flags

    func testUnmutingBindsZeroRatherThanNull() async throws {
        // `muted_until` is NOT NULL DEFAULT 0, and a DEFAULT applies only when the column
        // is omitted — not when NULL is bound to it. Binding nil would make "unmute this
        // chat" a constraint failure, which is why `SQLValue.millis` exists.
        try await store.upsertChat(chat("10", mutedUntil: Date().addingTimeInterval(3600)))
        try await store.setChatFlags("10", mutedUntil: nil, pinned: false, archived: false)

        let stored = try await store.chat(id: "10")
        XCTAssertNil(stored?.mutedUntil)
        XCTAssertFalse(stored?.isMuted ?? true)
    }

    func testAnUnmutedChatReadsBackAsNilNotAsNineteenSeventy() async throws {
        try await store.upsertChat(chat("10"))
        let stored = try await store.chat(id: "10")
        // `0` means absent. Read with the plain date accessor it would come back as
        // 1 January 1970 — which behaves correctly wherever it is only compared against
        // now, and renders as a date from before the app existed anywhere it is shown.
        XCTAssertNil(stored?.mutedUntil)
    }

    func testAMuteInTheFutureCountsAsMutedAndOneInThePastDoesNot() async throws {
        try await store.upsertChat(chat("future", mutedUntil: Date().addingTimeInterval(3600)))
        try await store.upsertChat(chat("past", mutedUntil: Date().addingTimeInterval(-3600)))

        let future = try await store.chat(id: "future")
        let past = try await store.chat(id: "past")
        // Derived from the deadline rather than stored, so a mute expires on its own with
        // nothing having to run at the moment it does.
        XCTAssertTrue(future?.isMuted ?? false)
        XCTAssertFalse(past?.isMuted ?? true)
    }

    func testAnEnumerationOverwritesTheFlagsOutright() async throws {
        try await store.upsertChat(chat("10", pinned: true, archived: true))
        // What `syncChatList` does with the server's answer. MAX-style merging would be
        // wrong here: unpinning sends false, and keeping the cached true would silently
        // refuse a change made on another device.
        try await store.upsertChat(chat("10", pinned: false, archived: false))

        let stored = try await store.chat(id: "10")
        XCTAssertEqual(stored?.isPinned, false)
        XCTAssertEqual(stored?.isArchived, false)
    }

    func testActivityNeverMovesBackwards() async throws {
        let late = Date(timeIntervalSince1970: 1_700_000_600)
        try await store.upsertChat(chat("10", activity: late))
        // A thinner write that knows nothing about activity — an implied chat from an
        // inbound message, say — must not drop the row to the bottom of the list.
        try await store.upsertChat(Chat(id: "10", kind: .direct, title: ""))

        let stored = try await store.chat(id: "10")
        XCTAssertEqual(stored?.lastActivityAt?.timeIntervalSince1970, late.timeIntervalSince1970)
    }

    func testSecretIsARealChatKind() async throws {
        try await store.upsertChat(chat("s1", kind: .secret))
        let stored = try await store.chat(id: "s1")
        XCTAssertEqual(stored?.kind, .secret)
        XCTAssertTrue(stored?.kind.isEndToEnd ?? false)
        // Two-party, so the row is named after its peer — and forgetting that `secret`
        // qualifies is why these rendered with a blank name.
        XCTAssertTrue(stored?.kind.isTwoParty ?? false)
    }

    // MARK: - Ratchet sessions

    private func session() -> SerializedSession {
        let pair = Crypto.generateKeyPair()
        return RatchetSession.responder(
            sharedSecret: Data(repeating: 0x2a, count: 32),
            signedPreKey: pair
        ).serialize()
    }

    func testSessionsAreStoredPerPeerDevice() async throws {
        let first = session()
        let second = session()
        try await store.saveSecretSession(peerUserID: "bob", peerDeviceID: "phone", state: first)
        try await store.saveSecretSession(peerUserID: "bob", peerDeviceID: "tablet", state: second)

        // Per DEVICE, not per peer. A shared session would leave the second device unable
        // to decrypt anything and would break the first as soon as it replied.
        let phone = try await store.secretSession(peerUserID: "bob", peerDeviceID: "phone")
        let tablet = try await store.secretSession(peerUserID: "bob", peerDeviceID: "tablet")
        XCTAssertEqual(phone, first)
        XCTAssertEqual(tablet, second)
        XCTAssertNotEqual(phone, tablet)
    }

    func testSavingTwiceReplacesRatherThanDuplicates() async throws {
        let old = session()
        let new = session()
        try await store.saveSecretSession(peerUserID: "bob", peerDeviceID: "phone", state: old)
        try await store.saveSecretSession(peerUserID: "bob", peerDeviceID: "phone", state: new)

        let stored = try await store.secretSession(peerUserID: "bob", peerDeviceID: "phone")
        XCTAssertEqual(stored, new)
        XCTAssertEqual(try await store.secretSessionDevices(peerUserID: "bob"), ["phone"])
    }

    func testForgettingASessionRemovesOnlyThatDevice() async throws {
        try await store.saveSecretSession(peerUserID: "bob", peerDeviceID: "phone", state: session())
        try await store.saveSecretSession(peerUserID: "bob", peerDeviceID: "tablet", state: session())
        try await store.forgetSecretSession(peerUserID: "bob", peerDeviceID: "phone")

        XCTAssertNil(try await store.secretSession(peerUserID: "bob", peerDeviceID: "phone"))
        XCTAssertNotNil(try await store.secretSession(peerUserID: "bob", peerDeviceID: "tablet"))
    }

    func testAMissingSessionIsNilNotAnError() async throws {
        XCTAssertNil(try await store.secretSession(peerUserID: "nobody", peerDeviceID: "none"))
    }

    // MARK: - Pending acks

    func testAcksAreRecordedAndClearedByID() async throws {
        try await store.noteSecretAck(queueID: "q1")
        try await store.noteSecretAck(queueID: "q2")
        XCTAssertEqual(Set(try await store.pendingSecretAcks()), ["q1", "q2"])

        try await store.clearSecretAcks(["q1"])
        XCTAssertEqual(try await store.pendingSecretAcks(), ["q2"])
    }

    func testNotingTheSameIDTwiceIsHarmless() async throws {
        // A queue row can be replayed, so the same id can arrive more than once. A second
        // insert must not fail the transaction that is also writing the plaintext.
        try await store.noteSecretAck(queueID: "q1")
        try await store.noteSecretAck(queueID: "q1")
        XCTAssertEqual(try await store.pendingSecretAcks(), ["q1"])
    }

    func testAnEmptyQueueIDIsIgnored() async throws {
        // A LIVE frame carries no queue id. Storing one would leave a row that can never
        // be acked, which the server would never drop.
        try await store.noteSecretAck(queueID: "")
        XCTAssertTrue(try await store.pendingSecretAcks().isEmpty)
    }

    // MARK: - Logout

    func testWipeClearsTheSecretTablesToo() async throws {
        try await store.upsertChat(chat("10", kind: .secret))
        try await store.saveSecretSession(peerUserID: "bob", peerDeviceID: "phone", state: session())
        try await store.noteSecretAck(queueID: "q1")

        try await store.wipe()

        // The reason `wipe()` enumerates `sqlite_master` instead of naming tables: the
        // hardcoded list stopped being complete the moment a migration added a table, and
        // what survived was one user's ratchet chain keys, on disk, for whoever signed in
        // next.
        XCTAssertNil(try await store.secretSession(peerUserID: "bob", peerDeviceID: "phone"))
        XCTAssertTrue(try await store.pendingSecretAcks().isEmpty)
        XCTAssertTrue(try await store.chatSummaries().isEmpty)
    }
}
