import Foundation
import SynapseDomain
import SynapseNetwork

/// The chat list, assembled locally.
///
/// The gateway has no "list my chats" message — `store.ListUserChats` exists
/// server-side but was never given a wire type — so the list is whatever the
/// cache has learned: chats we created, chats we joined, chats we resolved from
/// an `@handle`, and chats that sent us something. Every one of those paths
/// writes a chat row, which is why this repository reads only from SQLite and
/// never asks the network for a list it cannot serve.
public final class ChatRepositoryImpl: ChatRepository, @unchecked Sendable {
    private let client: SynapseClient
    private let store: LocalStore
    private let sync: SyncEngine

    public init(client: SynapseClient, store: LocalStore, sync: SyncEngine) {
        self.client = client
        self.store = store
        self.sync = sync
    }

    public func observeChats() -> AsyncStream<[ChatSummary]> {
        AsyncStream { continuation in
            let task = Task {
                for await _ in await store.changes(.chats) {
                    let summaries = (try? await store.chatSummaries()) ?? []
                    continuation.yield(summaries)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    public func chat(id: String) async -> Chat? {
        (try? await store.chat(id: id)) ?? nil
    }

    /// Resolves `@username` to its 1:1 chat and caches the mapping.
    ///
    /// The resolve happens over the protocol (see
    /// `SynapseClient.resolveDirectChat`); what happens here is the part the
    /// server cannot do for us — remembering that this chat id *is* that handle,
    /// so the list can render a name instead of a snowflake.
    public func openDirectChat(username: String) async throws -> Chat {
        let handle = OpenDirectChatUseCase.normalize(username)
        let chatID = try await ErrorMapping.mapped {
            try await client.resolveDirectChat(username: handle)
        }

        if let existing = try? await store.chat(id: chatID), let existing {
            return existing
        }

        // We know the chat and the handle but not the peer's user id: the
        // protocol never returns one for a handle. It gets filled in by the
        // first message the peer sends (see `SyncEngine.ingest`).
        let chat = Chat(id: chatID, kind: .direct, title: "@" + handle, username: handle)
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

    public func setMuted(chatID: String, muted: Bool) async {
        try? await store.setChatMuted(chatID, muted: muted)
    }

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
