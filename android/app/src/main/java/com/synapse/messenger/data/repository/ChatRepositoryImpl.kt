package com.synapse.messenger.data.repository

import com.synapse.messenger.core.AppError
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.core.runOutcome
import com.synapse.messenger.data.SessionHolder
import com.synapse.messenger.data.mapper.toDomain
import com.synapse.messenger.data.mapper.toWire
import com.synapse.messenger.data.sync.HistoryFetcher
import com.synapse.messenger.data.sync.MessageIngestor
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.domain.model.Chat
import com.synapse.messenger.domain.model.ChatKind
import com.synapse.messenger.domain.model.ChatTarget
import com.synapse.messenger.domain.repository.ChatRepository
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.ChatCreate
import com.synapse.messenger.network.protocol.ChatInfo
import com.synapse.messenger.network.protocol.Invites
import com.synapse.messenger.network.protocol.Join
import com.synapse.messenger.network.protocol.MsgType
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest

@Singleton
class ChatRepositoryImpl @Inject constructor(
    private val gateway: SynapseGateway,
    private val database: SynapseDatabase,
    private val history: HistoryFetcher,
    private val ingestor: MessageIngestor,
    private val sessionHolder: SessionHolder,
) : ChatRepository {

    private val chats get() = database.chatDao()
    private val users get() = database.userDao()

    override fun observeChats(): Flow<List<Chat>> = sessionHolder.userId.flatMapLatest { selfId ->
        // The local user directory is joined in memory rather than in SQL: it is
        // small, and a chat's label may come from a user learned through an entirely
        // different path (a contact add, a username we typed once).
        combine(chats.observeChatList(selfId), users.observeAll()) { rows, knownUsers ->
            val labels = knownUsers.associateBy({ it.userId }, { it.toDomain().displayLabel })
            rows.map { row -> row.toDomain { userId -> labels[userId] } }
        }
    }

    override fun observeChat(chatId: String): Flow<Chat?> =
        combine(chats.observeChat(chatId), users.observeAll()) { entity, knownUsers ->
            if (entity == null) return@combine null
            val chat = entity.toDomain()
            val peerLabel = chat.peerUserId?.let { peer ->
                knownUsers.firstOrNull { it.userId == peer }?.toDomain()?.displayLabel
            } ?: chat.peerUsername?.let { "@$it" }
            chat.copy(title = chat.title.ifEmpty { peerLabel.orEmpty() })
        }

    /**
     * Pull-to-refresh.
     *
     * There is no "sync my chats" request in this protocol, so a refresh is one
     * newest-page fetch per chat this device knows. Placeholder chats
     * (`"@username"`, not yet resolved server-side) are skipped deliberately: asking
     * for their history would make the gateway create a direct chat as a side effect
     * of a pull gesture.
     */
    override suspend fun refreshAll(): Outcome<Unit> = runOutcome {
        val ids = chats.allChatIds().filterNot { it.startsWith(PLACEHOLDER_PREFIX) }
        for (chatId in ids) {
            when (val outcome = refresh(chatId)) {
                is Outcome.Failure -> if (outcome.error is AppError.Offline) return@runOutcome Unit
                is Outcome.Success -> Unit
            }
        }
    }

    override suspend fun refresh(chatId: String): Outcome<Unit> = runOutcome {
        history.refreshNewest(chatId)
    }

    override suspend fun createGroup(
        title: String,
        memberRefs: List<String>,
        kind: ChatKind,
    ): Outcome<Chat> = runOutcome {
        val info: ChatInfo = gateway.request(
            MsgType.CHAT_CREATE,
            ChatCreate(
                type = kind.toWire(),
                title = title.trim(),
                // Members may be ids or "@username": the gateway resolves them, so a
                // client never has to look a stranger up before inviting them.
                members = memberRefs.map { it.trim() }.filter { it.isNotEmpty() },
            ),
        )
        chats.upsertKnown(
            chatId = info.chatId,
            type = info.type,
            title = info.title,
            ownerId = info.ownerId,
        )
        val stored = chats.findById(info.chatId)
        stored?.toDomain() ?: Chat(
            id = info.chatId,
            kind = ChatKind.GROUP,
            title = info.title,
        )
    }

    override suspend fun join(codeOrHandle: String): Outcome<String> = runOutcome {
        val trimmed = codeOrHandle.trim()
        // A handle names a CHAT here, not a person — the same "@" with a different
        // meaning than in a direct-chat address.
        val body = if (trimmed.startsWith("@")) {
            Join(handle = trimmed.removePrefix("@"))
        } else {
            Join(code = trimmed)
        }
        val result: Invites = gateway.request(MsgType.JOIN, body)
        val chatId = result.joinedChat
        check(chatId.isNotEmpty()) { "join returned no chat id" }
        chats.upsertKnown(chatId = chatId, type = MessageIngestor.TYPE_UNKNOWN)
        // A chat we just joined has history we have never seen.
        history.refreshNewest(chatId)
        chatId
    }

    /**
     * Opens a conversation with `@username`. Three cases, in order of cost:
     *
     *  1. We already hold the chat locally — nothing to ask anyone.
     *  2. We do not, so we probe for its newest message. This is the only way to
     *     learn a direct chat's id without sending something: HISTORY resolves
     *     `"@username"` server-side and the NEW frames it streams carry the real id.
     *     The resolution *creates* the chat if absent, which is why this only ever
     *     runs on an explicit user action.
     *  3. The chat resolved but is empty, so no frame carried an id. The conversation
     *     is then addressed by peer until the first send's ack names it — which is
     *     also what makes composing to a stranger work offline.
     */
    override suspend fun openDirectChat(username: String): Outcome<ChatTarget> = runOutcome {
        val handle = username.trim().removePrefix("@").lowercase()
        require(handle.isNotEmpty()) { "empty username" }

        chats.findDirectByUsername(handle)?.let { return@runOutcome ChatTarget.Existing(it.chatId) }

        val probe = history.page("$PLACEHOLDER_PREFIX$handle", beforeSeq = 0, limit = 1)
        val resolvedId = probe.messages.firstOrNull()?.chatId
        if (resolvedId == null) {
            ChatTarget.DirectPeer(handle)
        } else {
            ingestor.ingestAll(probe.messages)
            chats.upsertKnown(
                chatId = resolvedId,
                type = MessageIngestor.TYPE_DIRECT,
                peerUsername = handle,
            )
            // Anything composed offline under the placeholder belongs here now.
            ingestor.promotePlaceholder("$PLACEHOLDER_PREFIX$handle", resolvedId, handle)
            ChatTarget.Existing(resolvedId)
        }
    }

    override fun observeResolvedDirectChatId(username: String): Flow<String?> =
        chats.observeResolvedDirectChatId(username.removePrefix("@").lowercase())

    companion object {
        const val PLACEHOLDER_PREFIX = "@"
    }
}
