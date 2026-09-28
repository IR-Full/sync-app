import Foundation
import SyncAppDomain
import SyncAppNetwork

/// The chat list, read from the cache.
///
/// The authoritative list comes from `CHAT_LIST`, which `SyncEngine.syncChatList`
/// fetches on every connect and writes into SQLite. This repository therefore
/// still reads only from the cache — which is also what keeps the list current
/// between connects, since every path that mentions a chat (an inbound message, a
/// send ack, a create, a join, a resolved `@handle`) writes a row of its own.
public final class ChatRepositoryImpl: ChatRepository, @unchecked Sendable {
    private let client: SyncAppClient
    private let store: LocalStore
    private let sync: SyncEngine
    /// Present only in builds with the E2E module. Sending in a secret chat goes
    /// through it rather than through `SEND`, because the relay never sees plaintext.
    private let secret: SecretChatService?

    public init(
        client: SyncAppClient,
        store: LocalStore,
        sync: SyncEngine,
        secret: SecretChatService? = nil
    ) {
        self.client = client
        self.store = store
        self.sync = sync
        self.secret = secret
    }

    /// The chat list, with live typing folded in.
    ///
    /// `ChatSummary.typingUserIDs` comes from the sync engine, not from SQLite:
    /// typing is ephemeral and deliberately never stored. Two subscriptions feed
    /// this stream — one for the rows themselves and one for typing anywhere — and
    /// the merge is what makes the indicator work at all. The list previously read
    /// only `chatSummaries()`, whose `typingUserIDs` is always empty, so the row's
    /// "is someone typing" check could never be true.
    public func observeChats() -> AsyncStream<[ChatSummary]> {
        AsyncStream { continuation in
            let rows = Task {
                for await _ in await store.changes(.chats) {
                    continuation.yield(await currentSummaries())
                }
                continuation.finish()
            }
            let typing = Task {
                for await _ in await store.changes(.anyTyping) {
                    continuation.yield(await currentSummaries())
                }
            }
            continuation.onTermination = { _ in
                rows.cancel()
                typing.cancel()
            }
        }
    }

    /// The archived pile. No typing merge: nothing in the archive is being watched,
    /// and an indicator there would be noise on a screen nobody has open.
    public func observeArchivedChats() -> AsyncStream<[ChatSummary]> {
        AsyncStream { continuation in
            let rows = Task {
                continuation.yield((try? await store.chatSummaries(archived: true)) ?? [])
                for await _ in await store.changes(.chats) {
                    continuation.yield((try? await store.chatSummaries(archived: true)) ?? [])
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in rows.cancel() }
        }
    }

    /// The stored rows with live typing merged in.
    private func currentSummaries() async -> [ChatSummary] {
        let summaries = (try? await store.chatSummaries()) ?? []
        let typing = await sync.typingByChatSnapshot()
        guard !typing.isEmpty else { return summaries }
        return summaries.map { summary in
            guard let typists = typing[summary.id] else { return summary }
            var updated = summary
            updated.typingUserIDs = typists.sorted()
            return updated
        }
    }

    public func chat(id: String) async -> Chat? {
        (try? await store.chat(id: id)) ?? nil
    }

    /// Resolves `@username` to its 1:1 chat and caches the mapping.
    ///
    /// The resolve happens over the protocol (see
    /// `SyncAppClient.resolveDirectChat`); what happens here is the part the
    /// server cannot do for us — remembering that this chat id *is* that handle,
    /// so the list can render a name instead of a snowflake.
    public func openDirectChat(username: String) async throws -> Chat {
        let handle = OpenDirectChatUseCase.normalize(username)

        let chatID = try await ErrorMapping.mapped {
            try await client.resolveDirectChat(username: handle)
        }

        if let existing = try? await store.chat(id: chatID) {
            return existing
        }

        // We know the chat and the handle but not the peer's user id:
        // the protocol never returns one for a handle. It gets filled in
        // by the first message the peer sends.
        let chat = Chat(
            id: chatID,
            kind: .direct,
            title: "@" + handle,
            username: handle
        )

        try await store.upsertChat(chat)
        return chat
    }

    public func createGroup(title: String, memberHandles: [String], isChannel: Bool) async throws -> Chat {
        let info = try await ErrorMapping.mapped {
            try await client.createChat(
                type: isChannel ? "channel" : "group",
                title: title,
                members: memberHandles
            )
        }
        let chat = WireMapping.chat(from: info)
        try await store.upsertChat(chat)
        return chat
    }

    public func join(code: String?, handle: String?) async throws -> String {
        let chatID = try await ErrorMapping.mapped {
            try await client.join(code: code ?? "", handle: handle ?? "")
        }
        guard !chatID.isEmpty else { throw AppError.notFound }
        if ((try? await store.chat(id: chatID)) ?? nil) == nil {
            // A joined chat is a group or channel by construction — you cannot
            // join a 1:1 — so unlike an implied chat this kind is known.
            try await store.upsertChat(Chat(
                id: chatID,
                kind: .group,
                title: handle.map { "@" + OpenDirectChatUseCase.normalize($0) } ?? "",
                username: handle.map(OpenDirectChatUseCase.normalize)
            ))
        }
        // Pull the tail of the conversation so the chat is not empty on open.
        await backfill(chatID: chatID)
        return chatID
    }

    /// Creates a secret chat and publishes this device's keys if it has not already.
    ///
    /// The publish is not optional and not deferrable. A secret chat with a device the
    /// key directory does not list cannot receive anything, and the peer's send would
    /// report `undeliverable` — so the keys go up before the chat exists rather than
    /// whenever the next connect happens to come around.
    public func createSecretChat(peer: String) async throws -> Chat {
        await secret?.publishIfNeeded()

        let info = try await ErrorMapping.mapped {
            try await client.createSecretChat(with: peer)
        }
        var chat = WireMapping.chat(from: info)
        // `CHAT_INFO` carries no peer — it answers with the chat that was made, not
        // with its membership — and a two-party chat with no peer renders with no name
        // at all. So it is filled in from what the caller passed, but only when that
        // was an ID: a handle is not a user id, and storing one would make every later
        // peer lookup miss. For a handle the peer arrives with the next enumeration or
        // the first inbound message, whichever comes first.
        if !peer.hasPrefix("@"), !peer.isEmpty {
            chat.peerUserID = peer
        }
        chat.lastActivityAt = Date()
        try await store.upsertChat(chat)
        return chat
    }

    /// Writes the flags locally first, then sends, then reconciles with the echo.
    ///
    /// Optimistic because the alternative is a toggle that visibly lags a round trip.
    /// Reconciled because the echo is what the server actually stored — it clamps a
    /// mute deadline that is already past, and a client that kept its own value would
    /// show a chat as muted when it is not.
    public func setFlags(chatID: String, mutedUntil: Date?, pinned: Bool, archived: Bool) async {
        try? await store.setChatFlags(
            chatID, mutedUntil: mutedUntil, pinned: pinned, archived: archived
        )

        guard let echo = try? await client.setChatFlags(
            chatID: chatID,
            mutedUntil: mutedUntil.map { Int64($0.timeIntervalSince1970 * 1000) } ?? 0,
            pinned: pinned,
            archived: archived
        ) else {
            // Offline. The local write stands and the next enumeration overwrites it
            // with the server's version, so nothing is permanently inconsistent —
            // though the change does NOT reach other devices until then.
            return
        }

        try? await store.setChatFlags(
            chatID,
            mutedUntil: echo.mutedUntil > 0
                ? Date(timeIntervalSince1970: Double(echo.mutedUntil) / 1000)
                : nil,
            pinned: echo.pinned,
            archived: echo.archived
        )
    }

    public func setMuted(chatID: String, muted: Bool) async {
        // Kept for the callers that only want a toggle. "Muted" with no deadline means
        // muted for a very long time rather than forever: the server bounds the value,
        // and a deadline it has to clamp is worse than one already inside the bound.
        let existing = (try? await store.chat(id: chatID)) ?? nil
        await setFlags(
            chatID: chatID,
            mutedUntil: muted ? Date().addingTimeInterval(Self.longMute) : nil,
            pinned: existing?.isPinned ?? false,
            archived: existing?.isArchived ?? false
        )
    }

    /// Ten years. Long enough to read as "forever" and short enough to stay inside the
    /// server's bound on a mute deadline.
    private static let longMute: TimeInterval = 10 * 365 * 24 * 60 * 60

    /// Local-only. The protocol has no "leave chat" message, so calling this a
    /// delete would be a lie: the user stays a member and will reappear in the
    /// list on the next message.
    public func hideLocally(chatID: String) async {
        try? await store.setChatHidden(chatID, hidden: true)
    }

    private func backfill(chatID: String) async {
        guard let page = try? await client.history(chatID: chatID, beforeSeq: 0, limit: 50) else { return }
        await sync.ingest(messages: page.messages)
    }
}
