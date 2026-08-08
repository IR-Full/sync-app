package com.synapse.messenger.data.repository

import com.synapse.messenger.core.Outcome
import com.synapse.messenger.core.runOutcome
import com.synapse.messenger.data.SessionHolder
import com.synapse.messenger.data.mapper.toDomain
import com.synapse.messenger.data.mapper.toWire
import com.synapse.messenger.data.sync.ChatListSyncer
import com.synapse.messenger.data.sync.HistoryFetcher
import com.synapse.messenger.data.sync.MessageIngestor
import com.synapse.messenger.data.sync.ProfileFetcher
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
    private val chatListSyncer: ChatListSyncer,
    private val ingestor: MessageIngestor,
    private val profiles: ProfileFetcher,
    private val sessionHolder: SessionHolder,
) : ChatRepository {

    private val chats get() = database.chatDao()
    private val users get() = database.userDao()

    override fun observeChats(): Flow<List<Chat>> = sessionHolder.userId.flatMapLatest { selfId ->
        // The local user directory is joined in memory rather than in SQL: it is
        // small, and a chat's label may come from a person learned through an
        // entirely different path (a profile lookup, a contact sync).
        combine(chats.observeChatList(selfId), users.observeAll()) { rows, knownUsers ->
            val directory = knownUsers.associateBy({ it.userId }, { it.toDomain() })
            rows.map { row -> row.toDomain { userId -> directory[userId] } }
        }
    }

    override fun observeChat(chatId: String): Flow<Chat?> =
        combine(chats.observeChat(chatId), users.observeAll()) { entity, knownUsers ->
            if (entity == null) return@combine null
            val chat = entity.toDomain()
            val peer = chat.peerUserId?.let { id -> knownUsers.firstOrNull { it.userId == id } }?.toDomain()
            chat.copy(
                title = chat.title.ifEmpty {
                    peer?.displayLabel ?: chat.peerUsername?.let { "@$it" }.orEmpty()
                },
                peerAvatarRef = peer?.avatarRef,
            )
        }

    /**
     * Pull-to-refresh: the authoritative chat list, then whatever moved in it.
     *
     * CHAT_LIST is what makes a fresh install work — before it existed a client
     * could only learn about a chat by receiving traffic in it, so a reinstall
     * started blank and stayed blank. See [ChatListSyncer] for how a page turns into
     * rows and which chats are worth backfilling.
     */
    override suspend fun refreshAll(): Outcome<Unit> = runOutcome {
        chatListSyncer.sync()
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
     * Opens a conversation with `@username`, resolving it without side effects.
     *
     *  1. A chat we already hold for that handle — nothing to ask anyone.
     *  2. Otherwise the handle is resolved to a user through their profile, and the
     *     chat list tells us whether a direct chat with them exists. Neither request
     *     creates anything, which matters: addressing HISTORY to `"@username"` would
     *     have the gateway create the direct chat as a side effect of merely looking.
     *  3. No such chat yet, so the conversation is addressed by peer until the first
     *     send's ack names it — which is also what lets composing to a stranger work
     *     with no network at all.
     */
    override suspend fun openDirectChat(username: String): Outcome<ChatTarget> = runOutcome {
        val handle = username.trim().removePrefix("@").lowercase()
        require(handle.isNotEmpty()) { "empty username" }

        chats.findDirectByUsername(handle)?.let { return@runOutcome ChatTarget.Existing(it.chatId) }

        // Resolves the handle AND records the profile, so the chat has a name and an
        // avatar the moment it appears.
        val profile = profiles.fetch("$PLACEHOLDER_PREFIX$handle")
        // The peer id may already appear in a chat-list row we hold.
        chats.findDirectByPeer(profile.userId)?.let {
            ingestor.promotePlaceholder("$PLACEHOLDER_PREFIX$handle", it.chatId, handle)
            return@runOutcome ChatTarget.Existing(it.chatId)
        }
        // Not in our list: it may still exist server-side (created from another
        // device), and only a fresh list page can say so.
        runCatching { chatListSyncer.sync() }
        chats.findDirectByPeer(profile.userId)?.let {
            ingestor.promotePlaceholder("$PLACEHOLDER_PREFIX$handle", it.chatId, handle)
            return@runOutcome ChatTarget.Existing(it.chatId)
        }
        ChatTarget.DirectPeer(handle)
    }

    override fun observeResolvedDirectChatId(username: String): Flow<String?> =
        chats.observeResolvedDirectChatId(username.removePrefix("@").lowercase())

    companion object {
        const val PLACEHOLDER_PREFIX = "@"
    }
}
