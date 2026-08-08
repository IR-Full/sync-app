package com.synapse.messenger.domain.repository

import com.synapse.messenger.core.Outcome
import com.synapse.messenger.domain.model.Chat
import com.synapse.messenger.domain.model.ChatKind
import com.synapse.messenger.domain.model.ChatTarget
import com.synapse.messenger.domain.model.Message
import com.synapse.messenger.domain.model.MessageAttachment
import com.synapse.messenger.domain.model.Session
import com.synapse.messenger.domain.model.UserSummary
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow

/**
 * Repository contracts.
 *
 * Every one of these hides the protocol completely: no envelope, no message type,
 * no `"@username"` addressing convention crosses this boundary. A ViewModel that
 * calls [MessageRepository.sendText] cannot tell whether the transport is a binary
 * WebSocket frame or a REST call — which is the point, because the transport here
 * is unusual enough that leaking it would spread through every screen.
 */

/** Coarse connection state for the UI: enough to show "connecting", not a state machine. */
enum class ConnectionStatus { OFFLINE, CONNECTING, ONLINE }

interface AuthRepository {
    /** The signed-in identity, or null when logged out. Drives the start destination. */
    val session: StateFlow<Session?>

    /** True until the stored session has been read once, so no screen flashes wrongly. */
    val restored: StateFlow<Boolean>

    val connection: StateFlow<ConnectionStatus>

    suspend fun login(username: String, password: String): Outcome<Session>

    suspend fun register(username: String, password: String): Outcome<Session>

    /** Revokes locally: closes the socket, drops tokens, wipes the cache. */
    suspend fun logout()
}

interface ChatRepository {
    fun observeChats(): Flow<List<Chat>>

    fun observeChat(chatId: String): Flow<Chat?>

    /** Refreshes the newest page of every chat this device knows about. */
    suspend fun refreshAll(): Outcome<Unit>

    suspend fun refresh(chatId: String): Outcome<Unit>

    suspend fun createGroup(title: String, memberRefs: List<String>, kind: ChatKind): Outcome<Chat>

    /** Joins by invite code or `@handle`; returns the chat id the server let us into. */
    suspend fun join(codeOrHandle: String): Outcome<String>

    /**
     * Resolves a peer to something a chat screen can open. Existing conversations
     * resolve to their id; a stranger resolves to a peer reference that becomes an
     * id on the first send.
     */
    suspend fun openDirectChat(username: String): Outcome<ChatTarget>

    /**
     * Emits the direct chat's real id once the server has assigned one.
     *
     * A conversation opened on a stranger has no id until its first message is
     * acknowledged, so a screen showing it needs to be told when that changes rather
     * than polling for it.
     */
    fun observeResolvedDirectChatId(username: String): Flow<String?>
}

interface MessageRepository {
    fun observeMessages(chatId: String): Flow<List<Message>>

    /** How far the other members have read — the second tick on our own messages. */
    fun observeOthersReadSeq(chatId: String): Flow<Long>

    fun observeTyping(chatId: String): Flow<Set<String>>

    /**
     * Queues a message and tries to send it now. Succeeds offline: the message is
     * persisted in the outbox and flushed on reconnect, with the same dedup key, so
     * the server can never store it twice.
     */
    suspend fun sendText(
        target: ChatTarget,
        text: String,
        replyTo: String? = null,
    ): Outcome<Unit>

    suspend fun sendAttachment(
        target: ChatTarget,
        bytes: ByteArray,
        filename: String,
        mime: String,
        caption: String = "",
    ): Outcome<Unit>

    /** Retries a message that previously failed. */
    suspend fun retry(messageId: String): Outcome<Unit>

    /** Fetches one older page. Returns false when the server says there is no more. */
    suspend fun loadOlder(chatId: String, pageSize: Int = 50): Outcome<Boolean>

    suspend fun markRead(chatId: String, upToSeq: Long)

    fun sendTyping(chatId: String, active: Boolean)

    /** Flushes the outbox. Called when the connection becomes usable. */
    suspend fun flushOutbox()
}

interface UserRepository {
    fun observeKnownUsers(): Flow<List<UserSummary>>

    /**
     * Resolves `@username` to a user and records them as a contact.
     *
     * This doubles as the only user lookup the protocol offers: CONTACT_ADD is the
     * one request that takes a username and answers with a user id, so "find a
     * person" and "add a contact" are the same call.
     */
    suspend fun addContact(username: String, name: String? = null): Outcome<UserSummary>

    suspend fun syncContacts(): Outcome<Unit>

    suspend fun setBlocked(userIdOrUsername: String, blocked: Boolean): Outcome<Unit>
}

interface MediaRepository {
    /** A signed, expiring download URL for a media ref, cached until it expires. */
    suspend fun downloadUrl(mediaRef: String): Outcome<String>

    suspend fun upload(bytes: ByteArray, filename: String, mime: String): Outcome<MessageAttachment>
}
