package com.synapse.messenger.data.repository

import android.util.Log
import androidx.room.withTransaction
import com.synapse.messenger.core.AppError
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.core.runOutcome
import com.synapse.messenger.data.SessionHolder
import com.synapse.messenger.data.mapper.toDomain
import com.synapse.messenger.data.mapper.toStored
import com.synapse.messenger.data.mapper.toWire
import com.synapse.messenger.data.sync.HistoryFetcher
import com.synapse.messenger.data.sync.MessageIngestor
import com.synapse.messenger.data.sync.TypingTracker
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.database.entity.MessageEntity
import com.synapse.messenger.database.entity.MessageStatuses
import com.synapse.messenger.database.entity.OutboxEntity
import com.synapse.messenger.database.entity.StoredAttachment
import com.synapse.messenger.domain.model.ChatTarget
import com.synapse.messenger.domain.model.Message
import com.synapse.messenger.domain.model.MessageAttachment
import com.synapse.messenger.domain.repository.MediaRepository
import com.synapse.messenger.domain.repository.MessageRepository
import com.synapse.messenger.network.ConnectionState
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.MsgType
import com.synapse.messenger.network.protocol.ProtocolException
import com.synapse.messenger.network.protocol.Read
import com.synapse.messenger.network.protocol.Send
import com.synapse.messenger.network.protocol.SendAck
import com.synapse.messenger.network.protocol.Typing
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

@Singleton
class MessageRepositoryImpl @Inject constructor(
    private val gateway: SynapseGateway,
    private val database: SynapseDatabase,
    private val history: HistoryFetcher,
    private val ingestor: MessageIngestor,
    private val typingTracker: TypingTracker,
    private val mediaRepository: MediaRepository,
    private val sessionHolder: SessionHolder,
) : MessageRepository {

    private val messages get() = database.messageDao()
    private val chats get() = database.chatDao()
    private val outbox get() = database.outboxDao()
    private val receipts get() = database.readReceiptDao()

    /** One flush at a time: the outbox is an ordered queue, not a set. */
    private val flushLock = Mutex()

    @Volatile private var lastTypingSentAt = 0L

    override fun observeMessages(chatId: String): Flow<List<Message>> =
        combine(messages.observeMessages(chatId), sessionHolder.userId) { rows, selfId ->
            rows.map { it.toDomain(selfId) }
        }

    override fun observeOthersReadSeq(chatId: String): Flow<Long> =
        sessionHolder.userId.flatMapLatest { selfId ->
            receipts.observeOthersReadSeq(chatId, selfId)
        }

    override fun observeOthersDeliveredSeq(chatId: String): Flow<Long> =
        sessionHolder.userId.flatMapLatest { selfId ->
            receipts.observeOthersDeliveredSeq(chatId, selfId)
        }

    override fun observeTyping(chatId: String): Flow<Set<String>> = typingTracker.observe(chatId)

    // ------------------------------------------------------------------- sending

    /**
     * Composes a message and tries to send it immediately.
     *
     * This succeeds offline, and that is the whole design: the message is written
     * locally with a dedup key, queued, and flushed on reconnect. The gateway maps
     * (device, dedupKey) → message id, so however many times the flush retries, the
     * server stores it once — which is what makes "never lose a sent message" and
     * "never duplicate one" hold at the same time.
     */
    override suspend fun sendText(
        target: ChatTarget,
        text: String,
        replyTo: String?,
    ): Outcome<Unit> {
        val trimmed = text.trim()
        if (trimmed.isEmpty()) return Outcome.Failure(AppError.Rejected(0, "empty message"))
        // The gateway rejects anything longer; refusing here costs a round trip less
        // and keeps the message out of the outbox where it would retry forever.
        if (trimmed.toByteArray().size > MAX_TEXT_BYTES) {
            return Outcome.Failure(AppError.Rejected(0, "message too long"))
        }
        return enqueue(target = target, text = trimmed, attachment = null, replyTo = replyTo)
    }

    override suspend fun sendAttachment(
        target: ChatTarget,
        bytes: ByteArray,
        filename: String,
        mime: String,
        caption: String,
    ): Outcome<Unit> {
        // The bytes travel over HTTP, not the protocol: MEDIA_INIT mints a signed
        // upload URL, and only the resulting media_ref goes into the message. So an
        // attachment cannot be composed offline the way text can — there is nothing to
        // reference until the upload lands.
        val uploaded = mediaRepository.upload(bytes, filename, mime)
        val attachment = when (uploaded) {
            is Outcome.Failure -> return uploaded
            is Outcome.Success -> uploaded.value
        }
        return enqueue(target = target, text = caption.trim(), attachment = attachment, replyTo = null)
    }

    private suspend fun enqueue(
        target: ChatTarget,
        text: String,
        attachment: MessageAttachment?,
        replyTo: String?,
    ): Outcome<Unit> {
        val selfId = sessionHolder.currentUserId
        if (selfId.isEmpty()) return Outcome.Failure(AppError.Auth(null))

        val dedupKey = UUID.randomUUID().toString()
        val localId = LOCAL_ID_PREFIX + dedupKey
        val now = System.currentTimeMillis()
        val stored = attachment?.toStored()

        database.withTransaction {
            // An unresolved direct chat still needs somewhere to put the message, so
            // the placeholder chat is created here and promoted to a real id by the ack.
            if (target is ChatTarget.DirectPeer) {
                chats.upsertKnown(
                    chatId = target.ref,
                    type = MessageIngestor.TYPE_DIRECT,
                    peerUsername = target.username,
                    createdAt = now,
                )
            }
            messages.upsert(
                MessageEntity(
                    messageId = localId,
                    chatId = target.ref,
                    senderId = selfId,
                    seq = 0,
                    text = text,
                    attachmentJson = StoredAttachment.encode(stored),
                    replyTo = replyTo,
                    createdAt = now,
                    status = MessageStatuses.PENDING,
                    dedupKey = dedupKey,
                ),
            )
            outbox.enqueue(
                OutboxEntity(
                    dedupKey = dedupKey,
                    targetRef = target.ref,
                    text = text,
                    mediaRef = attachment?.mediaRef,
                    attachmentJson = StoredAttachment.encode(stored),
                    replyTo = replyTo,
                    createdAt = now,
                ),
            )
            chats.updateLastMessage(
                chatId = target.ref,
                text = text.ifEmpty { MessageIngestor.ATTACHMENT_PREVIEW },
                senderId = selfId,
                seq = 0,
                timestamp = now,
            )
        }

        flushOutbox()
        return Outcome.Success(Unit)
    }

    override suspend fun retry(messageId: String): Outcome<Unit> = runOutcome {
        val message = messages.findById(messageId) ?: return@runOutcome Unit
        val dedupKey = message.dedupKey ?: return@runOutcome Unit
        database.withTransaction {
            messages.updateStatus(messageId, MessageStatuses.PENDING)
            outbox.enqueue(
                OutboxEntity(
                    dedupKey = dedupKey,
                    targetRef = message.chatId,
                    text = message.text,
                    mediaRef = message.mediaRef,
                    attachmentJson = message.attachmentJson,
                    replyTo = message.replyTo,
                    createdAt = message.createdAt,
                ),
            )
        }
        flushOutbox()
    }

    /**
     * Drains the outbox in composition order.
     *
     * Order matters and is why this is sequential: two messages typed offline must
     * arrive in the order they were written, and only the server can assign the seq
     * that fixes that order for everyone.
     */
    override suspend fun flushOutbox() {
        if (gateway.state.value != ConnectionState.READY) return
        flushLock.withLock {
            for (entry in outbox.pending()) {
                if (gateway.state.value != ConnectionState.READY) return
                val localId = LOCAL_ID_PREFIX + entry.dedupKey
                try {
                    val ack: SendAck = gateway.request(
                        MsgType.SEND,
                        Send(
                            chatId = entry.targetRef,
                            dedupKey = entry.dedupKey,
                            text = entry.text,
                            mediaRef = entry.mediaRef.orEmpty(),
                            replyTo = entry.replyTo.orEmpty(),
                            attachment = StoredAttachment.decode(entry.attachmentJson)
                                ?.toDomain()
                                ?.toWire(),
                            ttlSeconds = entry.ttlSeconds,
                        ),
                    )
                    val peerUsername = entry.targetRef
                        .takeIf { it.startsWith(ChatRepositoryImpl.PLACEHOLDER_PREFIX) }
                        ?.removePrefix(ChatRepositoryImpl.PLACEHOLDER_PREFIX)
                    ingestor.ingestAck(
                        ack = ack,
                        localId = localId,
                        targetRef = entry.targetRef,
                        peerUsername = peerUsername,
                    )
                } catch (e: ProtocolException) {
                    if (e.isRetryable) {
                        // Throttling and server-class failures are exactly what the queue is
                        // for; leave the entry alone and let the next flush try again.
                        outbox.recordFailure(entry.dedupKey, e.message)
                        return
                    }
                    // A rejection a retry cannot fix (blocked, not a member, bad
                    // argument): stop pretending it is in flight and let the user see it.
                    Log.i(TAG, "send rejected permanently: ${e.message}")
                    database.withTransaction {
                        messages.updateStatus(localId, MessageStatuses.FAILED)
                        outbox.remove(entry.dedupKey)
                    }
                } catch (e: Exception) {
                    // Transport failure: the socket is gone or the request timed out.
                    // The entry stays queued; the reconnect will flush it.
                    outbox.recordFailure(entry.dedupKey, e.message)
                    return
                }
            }
        }
    }

    // ------------------------------------------------------------------- reading

    override suspend fun loadOlder(chatId: String, pageSize: Int): Outcome<Boolean> = runOutcome {
        history.loadOlder(chatId, pageSize)
    }

    /**
     * Advances our read cursor, locally and on the server.
     *
     * READ is answered only on failure, so there is nothing to await. The local
     * cursor moves first: the unread badge should clear the moment the user reads,
     * not a round trip later, and the server's copy exists to tell *other* people.
     */
    override suspend fun markRead(chatId: String, upToSeq: Long) {
        if (upToSeq <= 0) return
        val chat = chats.findById(chatId) ?: return
        if (chat.myReadSeq >= upToSeq) return
        chats.advanceReadCursor(chatId, upToSeq)
        // A placeholder chat does not exist server-side yet; there is no cursor to move.
        if (chatId.startsWith(ChatRepositoryImpl.PLACEHOLDER_PREFIX)) return
        runCatching {
            gateway.send(MsgType.READ, Read(chatId = chatId, upToChatSeq = upToSeq))
        }
    }

    /**
     * Sends a typing signal, throttled.
     *
     * The gateway throttles typing twice (per connection and per chat) and silently
     * drops the excess, so anything above roughly one frame per two seconds is
     * bandwidth spent on frames that will not be relayed.
     */
    override fun sendTyping(chatId: String, active: Boolean) {
        if (chatId.startsWith(ChatRepositoryImpl.PLACEHOLDER_PREFIX)) return
        val now = System.currentTimeMillis()
        if (active && now - lastTypingSentAt < TYPING_MIN_INTERVAL_MS) return
        lastTypingSentAt = now
        runCatching { gateway.send(MsgType.TYPING, Typing(chatId = chatId, active = active)) }
    }

    private companion object {
        const val TAG = "MessageRepository"
        const val LOCAL_ID_PREFIX = "local:"

        /** `message.MaxTextLen` on the server. */
        const val MAX_TEXT_BYTES = 8192
        const val TYPING_MIN_INTERVAL_MS = 2_000L
    }
}
