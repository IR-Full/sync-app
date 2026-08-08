import Foundation
import SynapseDomain
import SynapseNetwork

/// Messages: read from the cache, written optimistically, sent through the
/// outbox.
///
/// The send path is the whole offline story in one method. A message is written
/// to the cache *and* the outbox in the same breath, then handed to the network
/// — so composing works with the radio off, and the only difference a connection
/// makes is how quickly the row's state changes from `sending` to `sent`.
public final class MessageRepositoryImpl: MessageRepository, @unchecked Sendable {
    private let client: SynapseClient
    private let store: LocalStore
    private let sync: SyncEngine
    private let pageSize: Int32

    public init(client: SynapseClient, store: LocalStore, sync: SyncEngine, pageSize: Int32 = 50) {
        self.client = client
        self.store = store
        self.sync = sync
        self.pageSize = pageSize
    }

    public func observeMessages(chatID: String) -> AsyncStream<[Message]> {
        AsyncStream { continuation in
            let task = Task {
                for await _ in await store.changes(.messages(chatID: chatID)) {
                    let messages = (try? await store.messages(chatID: chatID)) ?? []
                    continuation.yield(messages)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    /// Pulls the newest page and merges it.
    ///
    /// `beforeSeq: 0` means "latest" on the server. The merge is safe to repeat:
    /// upserts are keyed by message id, so re-ingesting messages we already have
    /// changes nothing.
    public func refreshLatest(chatID: String) async throws {
        let page = try await ErrorMapping.mapped {
            try await client.history(chatID: chatID, beforeSeq: 0, limit: pageSize)
        }
        await sync.ingest(messages: page.messages)
    }

    /// Pulls one page older than what we hold.
    ///
    /// `beforeSeq` is the oldest server-assigned `seq` in the cache; `0` asks for
    /// the newest page, which is also what a chat opened for the first time
    /// wants. The returned `done` flag is the server telling us the page came up
    /// short — trust it rather than inferring from an empty result, because an
    /// empty page and a final page are different things.
    @discardableResult
    public func loadOlder(chatID: String) async throws -> Bool {
        let cursor = try await store.oldestSeq(chatID: chatID)
        let page = try await ErrorMapping.mapped {
            try await client.history(chatID: chatID, beforeSeq: cursor, limit: pageSize)
        }
        await sync.ingest(messages: page.messages)
        return !page.page.done
    }

    public func send(chatID: String, text: String, replyTo: String?, attachment: Attachment?) async throws {
        let dedupKey = UUID().uuidString
        let now = Date()

        // The optimistic row is keyed by the dedup key, so when the ack arrives
        // it becomes the same row rather than a second one to reconcile away.
        let optimistic = Message(
            id: dedupKey,
            chatID: chatID,
            senderID: await sync.userID,
            seq: 0,
            text: text,
            sentAt: now,
            state: .sending,
            replyToID: replyTo,
            attachment: attachment,
            dedupKey: dedupKey
        )
        try await store.upsertMessages([optimistic])
        try await store.enqueue(LocalStore.OutboxEntry(
            dedupKey: dedupKey, chatID: chatID, text: text,
            replyTo: replyTo, createdAt: now, attachment: attachment
        ))

        // Sending clears the draft — including on the user's other devices. Only
        // when there was one: an unconditional clear would double the frame
        // count of every send against a 20/sec limiter, to say nothing.
        if let existing = try? await store.draft(chatID: chatID), !existing.isEmpty {
            await saveDraft(chatID: chatID, text: "", replyTo: nil)
        }

        // Flushing the whole queue (rather than sending just this message) keeps
        // ordering right: if anything is already waiting, this message must go
        // out behind it, not ahead.
        await sync.flushOutbox()
    }

    public func retry(messageID: String) async throws {
        guard let message = try await store.message(id: messageID), !message.dedupKey.isEmpty else { return }
        try await store.enqueue(LocalStore.OutboxEntry(
            dedupKey: message.dedupKey,
            chatID: message.chatID,
            text: message.text,
            replyTo: message.replyToID,
            createdAt: message.sentAt,
            attachment: message.attachment
        ))
        try await store.setMessageState(id: messageID, state: .sending, chatID: message.chatID)
        await sync.flushOutbox()
    }

    /// Edits are answered only on failure, so the cache is updated immediately
    /// and the fanout that follows confirms it.
    public func edit(chatID: String, messageID: String, text: String) async throws {
        guard var message = try await store.message(id: messageID) else { return }
        message.text = text
        message.isEdited = true
        try await store.upsertMessages([message])
        try await ErrorMapping.mapped {
            try await client.editMessage(chatID: chatID, messageID: messageID, text: text)
        }
    }

    public func delete(chatID: String, messageID: String, forEveryone: Bool) async throws {
        guard var message = try await store.message(id: messageID) else { return }
        message.isDeleted = true
        message.text = ""
        try await store.upsertMessages([message])
        try await ErrorMapping.mapped {
            try await client.deleteMessage(chatID: chatID, messageID: messageID, forAll: forEveryone)
        }
    }

    /// Toggling is the server's semantics, not ours: sending the emoji you
    /// already have removes it, and a different one replaces it. The reply
    /// carries the post-change tally, so there is nothing to compute locally.
    public func toggleReaction(chatID: String, messageID: String, emoji: String) async throws {
        let update = try await ErrorMapping.mapped {
            try await client.react(chatID: chatID, messageID: messageID, emoji: emoji)
        }
        try await store.applyReactions(
            chatID: chatID, messageID: messageID, counts: update.counts.mapValues(Int.init)
        )
    }

    public func markRead(chatID: String, upToSeq: UInt64) async {
        // Local first: the badge should clear the instant the user looks at the
        // chat, whether or not the receipt reaches the server.
        try? await store.markRead(chatID: chatID, upToSeq: upToSeq)
        let messageID = (try? await store.messages(chatID: chatID))?
            .last(where: { $0.seq == upToSeq })?.id ?? ""
        try? await client.markRead(chatID: chatID, upToMessageID: messageID, upToChatSeq: upToSeq)
    }

    public func setTyping(chatID: String, active: Bool) async {
        try? await client.setTyping(chatID: chatID, active: active)
    }

    public func observeTyping(chatID: String) -> AsyncStream<Set<String>> {
        AsyncStream { continuation in
            let task = Task {
                for await _ in await store.changes(.typing(chatID: chatID)) {
                    continuation.yield(await sync.typingUsers(chatID: chatID))
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    /// Saves locally first, then mirrors to the server.
    ///
    /// `DRAFT_SET` is answered only on failure and the gateway routes the mirror
    /// to this user's *other* devices, never back to us — so the local write is
    /// not an optimistic guess, it is the only copy this device will get.
    public func saveDraft(chatID: String, text: String, replyTo: String?) async {
        try? await store.upsertDraft(
            chatID: chatID, text: text, replyTo: replyTo, updatedAt: Date()
        )
        try? await client.setDraft(chatID: chatID, text: text, replyTo: replyTo ?? "")
    }

    public func observeDraft(chatID: String) -> AsyncStream<String> {
        AsyncStream { continuation in
            let task = Task {
                for await _ in await store.changes(.draft(chatID: chatID)) {
                    continuation.yield((try? await store.draft(chatID: chatID)) ?? "")
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
}
