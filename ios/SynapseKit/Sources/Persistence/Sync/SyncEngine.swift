import Foundation
import OSLog
import SynapseDomain
import SynapseNetwork

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
    private let client: SynapseClient
    private let store: LocalStore
    private let broker: ChangeBroker
    private let log = Logger(subsystem: "chat.synapse.ios", category: "sync")

    private var eventPump: Task<Void, Never>?
    private var flushTask: Task<Void, Never>?
    private var typingByChat: [String: [String: Date]] = [:]
    private var typingSweeper: Task<Void, Never>?

    public private(set) var userID: String = ""
    public private(set) var status: ConnectionStatus = .offline
    private var statusSubscribers: [UUID: AsyncStream<ConnectionStatus>.Continuation] = [:]
    private var expirySubscribers: [UUID: AsyncStream<Void>.Continuation] = [:]

    public init(client: SynapseClient, store: LocalStore, broker: ChangeBroker) {
        self.client = client
        self.store = store
        self.broker = broker
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

    private func dropStatusSubscriber(_ id: UUID) { statusSubscribers[id] = nil }
    private func dropExpirySubscriber(_ id: UUID) { expirySubscribers[id] = nil }

    private func setStatus(_ new: ConnectionStatus) {
        guard status != new else { return }
        status = new
        for continuation in statusSubscribers.values { continuation.yield(new) }
        Task { await broker.notify(.connection) }
    }

    // MARK: - Event handling

    private func handle(_ event: SynapseClient.Event) async {
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

        case .error(let error):
            log.error("uncorrelated protocol error \(error.code.rawValue): \(error.message)")

        case .sessionExpired:
            setStatus(.offline)
            for continuation in expirySubscribers.values { continuation.yield(()) }
        }
    }

    private func handleStateChange(_ state: SynapseClient.ConnectionState) async {
        switch state {
        case .ready:
            setStatus(.online)
            // Reconnected: anything composed offline goes out now, and the
            // address book catches up. Both are cheap and both are wrong to
            // defer until the user notices.
            scheduleFlush()
            await syncContacts()
            await syncDrafts()
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
        Task { await broker.notify(.typing(chatID: chatID)) }
    }

    public func typingUsers(chatID: String) -> Set<String> {
        let cutoff = Date().addingTimeInterval(-Self.typingTTL)
        return Set((typingByChat[chatID] ?? [:]).filter { $0.value > cutoff }.keys)
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
                await broker.notify(.typing(chatID: chatID))
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
