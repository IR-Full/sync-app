import Foundation
import OSLog
import SyncAppDomain
import SyncAppNetwork

/// The seam between the live connection and the cache.
///
/// Everything the gateway pushes lands here and is written to SQLite; the UI
/// reads only from SQLite. That one rule is what makes the app offline-first
/// rather than offline-tolerant — there is no code path where the screen shows
/// something the cache does not have, so "no connection" changes how fresh the
/// data is and nothing else.
///
/// It also owns the two jobs that have to happen at reconnect: flushing the
/// outbox and re-syncing the address book.
public actor SyncEngine {
    private let client: SyncAppClient
    private let store: LocalStore
    private let broker: ChangeBroker
    private let log = Logger(subsystem: "chat.syncapp.ios", category: "sync")

    private var eventPump: Task<Void, Never>?
    private var flushTask: Task<Void, Never>?
    private var typingByChat: [String: [String: Date]] = [:]
    private var typingSweeper: Task<Void, Never>?

    public private(set) var userID: String = ""
    public private(set) var status: ConnectionStatus = .offline
    private var statusSubscribers: [UUID: AsyncStream<ConnectionStatus>.Continuation] = [:]
    private var expirySubscribers: [UUID: AsyncStream<Void>.Continuation] = [:]
    private var profileSubscribers: [UUID: AsyncStream<ProfileBody>.Continuation] = [:]
    private var subscriptionSubscribers: [UUID: AsyncStream<SubscriptionBody>.Continuation] = [:]

    /// Secret chats. Optional so a build without the E2E module still compiles and runs
    /// — and so a test can drive the engine without a Keychain.
    ///
    /// When it is nil, inbound `SECRET_RECV` frames are logged and dropped rather than
    /// silently discarded. That distinction matters: a dropped secret message is
    /// invisible to both ends, and a log line is the only thing that says the build
    /// received something it was not equipped to open.
    private var secret: SecretChatService?

    /// The entitlements last heard from the server.
    ///
    /// Cached because it arrives as a PUSH as well as a reply: a subscription lapses
    /// without this client asking, and a UI that gates on a value it fetched at launch
    /// goes on offering features the server has started refusing.
    public private(set) var entitlements: SubscriptionBody?

    public init(client: SyncAppClient, store: LocalStore, broker: ChangeBroker) {
        self.client = client
        self.store = store
        self.broker = broker
    }

    /// Attaches the secret-chat pipeline. Separate from `init` because the service
    /// needs the device id, which the app resolves after the engine is built.
    public func attach(secret: SecretChatService) {
        self.secret = secret
    }

    /// Starts consuming protocol events. Idempotent.
    public func start(userID: String) {
        self.userID = userID
        guard eventPump == nil else { return }

        eventPump = Task { [weak self] in
            guard let self else { return }
            for await event in await self.client.events() {
                await self.handle(event)
            }
        }
        startTypingSweeper()
    }

    public func stop() {
        eventPump?.cancel()
        eventPump = nil
        flushTask?.cancel()
        flushTask = nil
        typingSweeper?.cancel()
        typingSweeper = nil
        typingByChat.removeAll()
        setStatus(.offline)
    }

    // MARK: - Status

   public func connectionStatus() -> AsyncStream<ConnectionStatus> {
        let id = UUID()
        let (stream, continuation) =
            AsyncStream<ConnectionStatus>.makeStream()

        statusSubscribers[id] = continuation
        continuation.yield(status)

        let engine = self

        continuation.onTermination = { [weak engine, id] _ in
            Task { [weak engine, id] in
                await engine?.dropStatusSubscriber(id)
            }
        }

        return stream
    }

    public func sessionExpirations() -> AsyncStream<Void> {
        let id = UUID()
        let (stream, continuation) =
            AsyncStream<Void>.makeStream()

        expirySubscribers[id] = continuation

        let engine = self

        continuation.onTermination = { [weak engine, id] _ in
            Task { [weak engine, id] in
                await engine?.dropExpirySubscriber(id)
            }
        }

        return stream
    }

    /// Our own profile, whenever the gateway says it changed.
    ///
    /// A `PROFILE` frame with no request id is a change made on another device
    /// of this account — the gateway routes it per user, exactly like a draft.
    public func profileUpdates() -> AsyncStream<ProfileBody> {
        let id = UUID()
        let (stream, continuation) = AsyncStream<ProfileBody>.makeStream()

        profileSubscribers[id] = continuation

        let engine = self

        continuation.onTermination = { [weak engine, id] _ in
            Task { [weak engine, id] in
                await engine?.dropProfileSubscriber(id)
            }
        }

        return stream
    }

    private func dropStatusSubscriber(_ id: UUID) { statusSubscribers[id] = nil }
    private func dropExpirySubscriber(_ id: UUID) { expirySubscribers[id] = nil }
    private func dropProfileSubscriber(_ id: UUID) { profileSubscribers[id] = nil }

    private func setStatus(_ new: ConnectionStatus) {
        guard status != new else { return }
        status = new
        for continuation in statusSubscribers.values { continuation.yield(new) }
        Task { await broker.notify(.connection) }
    }

    // MARK: - Event handling

    private func handle(_ event: SyncAppClient.Event) async {
        switch event {
        case .state(let state):
            await handleStateChange(state)

        case .message(let body):
            await ingest(messages: [body])

        case .sendAck(let body):
            await applySendAck(body)

        case .readReceipt(let body):
            // A receipt from *us* (another device of ours) moves our own read
            // marker; a receipt from a peer marks our messages as read. The two
            // are the same frame and mean opposite things depending on who sent
            // it, which is easy to get backwards.
            if body.userID == userID {
                try? await store.markRead(chatID: body.chatID, upToSeq: body.upToChatSeq)
            } else {
                try? await store.applyReadReceipt(
                    chatID: body.chatID, upToSeq: body.upToChatSeq, ourUserID: userID
                )
            }

        case .typing(let body):
            noteTyping(chatID: body.chatID, userID: body.userID, active: body.active)

        case .presence(let body):
            try? await store.upsertUser(User(
                id: body.userID,
                isOnline: body.online,
                lastSeenAt: WireMapping.date(millis: body.lastSeenMs)
            ))

        case .reaction(let body):
            try? await store.applyReactions(
                chatID: body.chatID,
                messageID: body.messageID,
                counts: body.counts.mapValues(Int.init)
            )

        case .chatInfo(let body):
            try? await store.upsertChat(WireMapping.chat(from: body))

        case .pinned:
            break  // Pins are fetched on demand by the chat screen.

        case .profile(let body):
            // Only ever our own — the gateway mirrors PROFILE_SET per user. The
            // id check costs nothing and keeps a future change of that routing
            // from quietly rewriting our identity with someone else's.
            guard body.userID == userID else { break }
            for continuation in profileSubscribers.values { continuation.yield(body) }

        case .delivered:
            break  // No delivered tick in the UI yet; the frame is not an error.

        case .drafts(let body):
            for draft in body.drafts {
                try? await store.upsertDraft(
                    chatID: draft.chatID,
                    text: draft.text,
                    replyTo: draft.replyTo.nilIfEmpty,
                    updatedAt: WireMapping.date(millis: draft.updatedAt) ?? Date()
                )
            }
            if body.cursor > 0 {
                try? await store.setMeta(LocalStore.MetaKey.draftCursor, String(body.cursor))
            }

        case .secretMessage(let body):
            guard let secret else {
                // Not a silent drop. A build with no secret pipeline can still be
                // ADDRESSED by one that has it, and "a message arrived that this
                // version cannot open" is the only useful thing to say about it.
                log.notice("received a secret frame but no secret-chat service is attached")
                break
            }
            await secret.receive(body, ourUserID: userID)

        case .subscription(let body):
            entitlements = body
            for continuation in subscriptionSubscribers.values { continuation.yield(body) }

        case .error(let error):
            log.error("uncorrelated protocol error \(error.code.rawValue): \(error.message)")

        case .sessionExpired:
            setStatus(.offline)
            for continuation in expirySubscribers.values { continuation.yield(()) }
        }
    }

    private func handleStateChange(_ state: SyncAppClient.ConnectionState) async {
        switch state {
        case .ready:
            setStatus(.online)
            // Reconnected: anything composed offline goes out now, and the
            // address book catches up. Both are cheap and both are wrong to
            // defer until the user notices.
            scheduleFlush()
            await flushPushToken()
            await syncChatList()
            await syncContacts()
            await syncDrafts()
            await syncEntitlements()
            // Keys first, then the replay. In that order because publishing is what
            // makes this device addressable at all: draining the queue before the
            // directory knows about the device would drain whatever accumulated for a
            // device the peers could not see.
            if let secret {
                await secret.publishIfNeeded()
                await secret.syncQueue()
            }
            try? await store.purgeExpired()
        case .connecting, .authenticating, .reconnecting:
            setStatus(.connecting)
        case .idle, .closed:
            setStatus(.offline)
        }
    }

    // MARK: - Ingest

    /// Writes messages and the chats they imply.
    public func ingest(messages bodies: [NewMessageBody]) async {
        guard !bodies.isEmpty else { return }
        let messages = bodies.map { WireMapping.message(from: $0, ourUserID: userID) }

        // The chat row must exist before the messages: there is a foreign key,
        // and a message whose chat we have never heard of is the normal case for
        // the first message of a new conversation.
        for chatID in Set(messages.map(\.chatID)) {
            guard let sample = messages.first(where: { $0.chatID == chatID }) else { continue }
            let existing = (try? await store.chat(id: chatID)) ?? nil
            if var chat = existing {
                // Fill in the peer we may not have known yet.
                if chat.peerUserID == nil, chat.kind == .direct, sample.senderID != userID {
                    chat.peerUserID = sample.senderID
                    try? await store.upsertChat(chat)
                }
            } else {
                try? await store.upsertChat(
                    WireMapping.impliedChat(from: sample, ourUserID: userID)
                )
            }
            // Learn the sender as a user we have seen, so names can resolve later.
            for senderID in Set(messages.filter { $0.chatID == chatID }.map(\.senderID))
            where senderID != userID && !senderID.isEmpty {
                try? await store.upsertUser(User(id: senderID))
            }
        }

        try? await store.upsertMessages(messages)
    }

    private func applySendAck(_ ack: SendAckBody) async {
        guard !ack.dedupKey.isEmpty else { return }
        try? await store.confirmMessage(
            dedupKey: ack.dedupKey,
            messageID: ack.messageID,
            chatID: ack.chatID,
            seq: ack.chatSeq,
            timestamp: WireMapping.date(millis: ack.timestamp) ?? Date()
        )
    }

    // MARK: - Typing

    /// Typing is ephemeral and the server sends no "stopped" for a client that
    /// simply went away, so each signal is kept with an expiry and swept. Six
    /// seconds is a little over the gateway's own per-chat throttle (one signal
    /// every two seconds), so a continuously-typing peer never flickers off.
    private static let typingTTL: TimeInterval = 6

    private func noteTyping(chatID: String, userID typist: String, active: Bool) {
        guard !typist.isEmpty, typist != userID else { return }
        if active {
            typingByChat[chatID, default: [:]][typist] = Date()
        } else {
            typingByChat[chatID]?[typist] = nil
        }
        Task { await broker.notify([.typing(chatID: chatID), .anyTyping]) }
    }

    public func typingUsers(chatID: String) -> Set<String> {
        let cutoff = Date().addingTimeInterval(-Self.typingTTL)
        return Set((typingByChat[chatID] ?? [:]).filter { $0.value > cutoff }.keys)
    }

    /// Who is typing, across every chat with a live signal. The chat list needs
    /// all of them at once; asking per row would mean one actor hop per row on
    /// every redraw.
    public func typingByChatSnapshot() -> [String: Set<String>] {
        let cutoff = Date().addingTimeInterval(-Self.typingTTL)
        var out: [String: Set<String>] = [:]
        for (chatID, typists) in typingByChat {
            let live = Set(typists.filter { $0.value > cutoff }.keys)
            if !live.isEmpty { out[chatID] = live }
        }
        return out
    }

    private func startTypingSweeper() {
        typingSweeper?.cancel()
        typingSweeper = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                await self?.sweepTyping()
            }
        }
    }

    private func sweepTyping() async {
        let cutoff = Date().addingTimeInterval(-Self.typingTTL)
        for (chatID, typists) in typingByChat {
            let live = typists.filter { $0.value > cutoff }
            if live.count != typists.count {
                typingByChat[chatID] = live.isEmpty ? nil : live
                await broker.notify([.typing(chatID: chatID), .anyTyping])
            }
        }
    }

    // MARK: - Outbox

    /// Sends everything queued, oldest first.
    ///
    /// Serial on purpose. The gateway allocates a gap-free `chat_seq` per chat
    /// in arrival order, and the connection is flood-limited to 20 sends/sec —
    /// firing a backlog concurrently would both reorder the user's own messages
    /// and trip the limiter.
    public func flushOutbox() async {
        guard status == .online else { return }
        let pending = (try? await store.pendingOutbox()) ?? []
        guard !pending.isEmpty else { return }

        for entry in pending {
            do {
                let attachment = WireMapping.attachmentBody(from: entry.attachment)
                let ack = try await client.sendMessage(
                    chatID: entry.chatID,
                    dedupKey: entry.dedupKey,
                    text: entry.text,
                    replyTo: entry.replyTo ?? "",
                    // `media_ref` is duplicated outside the attachment for
                    // backward compatibility with plain media messages — the
                    // server's own model keeps both.
                    mediaRef: attachment?.mediaRef ?? "",
                    attachment: attachment
                )
                // `duplicate == true` means an earlier attempt already landed and
                // this retry resolved to it. That is a success, not a conflict —
                // it is exactly what the dedup key is for.
                await applySendAck(ack)
            } catch let error as ProtocolError where !error.isRetryable {
                // Forbidden, blocked, chat gone: retrying cannot help, so stop
                // holding the message hostage and show it as failed.
                log.error("outbox entry \(entry.dedupKey) rejected: \(error.message)")
                try? await store.recordOutboxFailure(dedupKey: entry.dedupKey, error: error.message)
                try? await store.removeFromOutbox(dedupKey: entry.dedupKey)
                try? await store.setMessageState(
                    id: entry.dedupKey, state: .failed, chatID: entry.chatID
                )
            } catch {
                // Transport or throttle: leave it queued and try on the next
                // reconnect. Stop the run — if one send cannot get through, the
                // rest will not either, and hammering wastes the radio.
                log.notice("outbox flush paused at \(entry.dedupKey): \(String(describing: error))")
                try? await store.recordOutboxFailure(
                    dedupKey: entry.dedupKey, error: String(describing: error)
                )
                return
            }
        }
    }

    private func scheduleFlush() {
        flushTask?.cancel()
        flushTask = Task { [weak self] in
            await self?.flushOutbox()
        }
    }

    // MARK: - Contacts

    /// Incremental sync: the server returns everything changed after our cursor,
    /// so a full download happens exactly once per install.
    /// Delivers the push token the app wants the server to hold, if it differs
    /// from the one the server has acknowledged.
    ///
    /// Called both when the token changes and on every connect, because the two
    /// events are unordered: APNs commonly hands over a token before a connection
    /// exists, and turning notifications off while offline still has to reach the
    /// server eventually — otherwise the push keeps arriving.
    public func flushPushToken() async {
        let desired = (try? await store.meta(LocalStore.MetaKey.desiredPushToken)) ?? nil
        guard let desired else { return } // never set: nothing to deliver
        let acked = (try? await store.meta(LocalStore.MetaKey.ackedPushToken)) ?? nil
        guard desired != acked else { return }
        do {
            try await client.registerPushToken(desired)
            try? await store.setMeta(LocalStore.MetaKey.ackedPushToken, desired)
        } catch {
            // Left un-acked on purpose, so the next connect tries again.
            log.notice("push token registration deferred: \(String(describing: error))")
        }
    }

    /// Enumerates the chats this account belongs to and reconciles the cache.
    ///
    /// This is what a fresh install needs: every other path learns about a chat as
    /// a consequence of traffic, so without this a new device opens to an empty
    /// list and stays empty until somebody writes to it. Run on every connect, not
    /// just the first, because a chat joined from another device is otherwise
    /// invisible here too.
    ///
    /// Server fields win where the server is authoritative — type, title, owner,
    /// handle, `lastSeq`, and now the mute/pin/archive flags, the preview and the
    /// activity timestamp, all of which the enumeration carries. Only the READ CURSOR
    /// stays local, because the server does not send it and overwriting it with nothing
    /// would reset every unread badge on every connect.
    public func syncChatList() async {
        do {
            let summaries = try await client.allChats()
            for summary in summaries where !summary.chatID.isEmpty {
                var chat = WireMapping.chat(from: summary)
                if let cached = (try? await store.chat(id: chat.id)) ?? nil {
                    chat.lastReadSeq = cached.lastReadSeq
                    // The mute/pin/archive flags are NOT restored from the cache any
                    // more: the server now stores them per member and sends them with
                    // every enumeration, so it is authoritative and keeping the cached
                    // copy would silently revert a change made on another device.
                    //
                    // The preview and activity are still kept when the server sends
                    // nothing, because an empty value there means "not included".
                    if chat.lastMessagePreview.isEmpty {
                        chat.lastMessagePreview = cached.lastMessagePreview
                        chat.lastMessageAt = cached.lastMessageAt
                    }
                    if chat.lastActivityAt == nil { chat.lastActivityAt = cached.lastActivityAt }
                    // A direct chat's peer is filled in locally when the server
                    // does not name one, so do not drop what we already resolved.
                    if chat.peerUserID == nil { chat.peerUserID = cached.peerUserID }
                    // The server's lastSeq can only move forward; a cached value
                    // ahead of it means we ingested a message it has not counted yet.
                    chat.lastSeq = max(chat.lastSeq, cached.lastSeq)
                }
                try? await store.upsertChat(chat)
            }
        } catch let error as ProtocolError where error.code == .unsupported {
            // An older gateway has no CHAT_LIST; the locally-assembled list stands.
        } catch {
            log.notice("chat list sync failed: \(String(describing: error))")
        }
    }

    /// A stream of subscription changes, for the screens that gate on entitlements.
    public func subscriptions() -> AsyncStream<SubscriptionBody> {
        let id = UUID()
        let (stream, continuation) = AsyncStream<SubscriptionBody>.makeStream()
        subscriptionSubscribers[id] = continuation
        if let entitlements { continuation.yield(entitlements) }

        let engine = self
        continuation.onTermination = { [weak engine, id] _ in
            Task { [weak engine, id] in
                await engine?.dropSubscriptionSubscriber(id)
            }
        }
        return stream
    }

    private func dropSubscriptionSubscriber(_ id: UUID) { subscriptionSubscribers[id] = nil }

    /// Reads the tier once per connect, so a screen has something to gate on before the
    /// first push arrives.
    ///
    /// A gateway with no billing service still ANSWERS this, with everything granted —
    /// it does not error — so the `unsupported` branch below is only for a gateway too
    /// old to know the type at all. There the value is left absent, which callers read
    /// as "not yet known" rather than as "not entitled".
    public func syncEntitlements() async {
        do {
            let body = try await client.subscription()
            entitlements = body
            for continuation in subscriptionSubscribers.values { continuation.yield(body) }
        } catch let error as ProtocolError where error.code == .unsupported {
            entitlements = nil
        } catch {
            log.notice("subscription sync failed: \(String(describing: error))")
        }
    }

    public func syncContacts() async {
        do {
            let since = Int64((try? await store.meta(LocalStore.MetaKey.contactCursor)) ?? "") ?? 0
            let page = try await client.syncContacts(since: since)
            let contacts = page.contacts.map(WireMapping.contact(from:))
            if !contacts.isEmpty {
                try await store.upsertContacts(contacts)
                for contact in contacts where !contact.name.isEmpty {
                    try? await store.upsertUser(User(id: contact.userID, displayName: contact.name))
                }
            }
            if page.cursor > 0 {
                try await store.setMeta(LocalStore.MetaKey.contactCursor, String(page.cursor))
            }
        } catch let error as ProtocolError where error.code == .unsupported {
            // Contacts are optional in the server's service wiring.
        } catch {
            log.notice("contact sync failed: \(String(describing: error))")
        }
    }

    /// Pulls drafts changed since our cursor, so composing continues on this
    /// device where another one left off.
    public func syncDrafts() async {
        do {
            let since = Int64((try? await store.meta(LocalStore.MetaKey.draftCursor)) ?? "") ?? 0
            let page = try await client.syncDrafts(since: since)
            for draft in page.drafts {
                try await store.upsertDraft(
                    chatID: draft.chatID,
                    text: draft.text,
                    replyTo: draft.replyTo.nilIfEmpty,
                    updatedAt: WireMapping.date(millis: draft.updatedAt) ?? Date()
                )
            }
            if page.cursor > 0 {
                try await store.setMeta(LocalStore.MetaKey.draftCursor, String(page.cursor))
            }
        } catch let error as ProtocolError where error.code == .unsupported {
            // Drafts share the server's optional pin service.
        } catch {
            log.notice("draft sync failed: \(String(describing: error))")
        }
    }
}
