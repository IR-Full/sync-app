package com.synapse.messenger.data.sync

import androidx.room.withTransaction
import com.synapse.messenger.data.SessionHolder
import com.synapse.messenger.data.mapper.toEntity
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.database.entity.MessageEntity
import com.synapse.messenger.database.entity.MessageStatuses
import com.synapse.messenger.database.entity.ReadReceiptEntity
import com.synapse.messenger.network.protocol.NewMessage
import com.synapse.messenger.network.protocol.ReadUpdate
import com.synapse.messenger.network.protocol.SendAck
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The single place a server-originated message fact becomes a local row.
 *
 * Live fanout and history backfill deliver the *same* NEW frame shape — the
 * gateway replays stored messages as ordinary NEW frames so a client's normal
 * ingest path handles them — so they must share one writer, or the two paths would
 * drift on details like "does this bump the chat's last message".
 *
 * Everything here is idempotent. It has to be: a message can legitimately arrive
 * twice (fanout now, backfill later), and the gateway also fans a message back to
 * its own sender's devices, so this device sees its own sends echoed.
 */
@Singleton
class MessageIngestor @Inject constructor(
    private val db: SynapseDatabase,
    private val sessionHolder: SessionHolder,
) {
    private val chats get() = db.chatDao()
    private val messages get() = db.messageDao()
    private val outbox get() = db.outboxDao()
    private val receipts get() = db.readReceiptDao()

    /** Writes one delivered or backfilled message. */
    suspend fun ingest(body: NewMessage, bumpChat: Boolean = true) = ingestAll(listOf(body), bumpChat)

    suspend fun ingestAll(bodies: List<NewMessage>, bumpChat: Boolean = true) {
        if (bodies.isEmpty()) return
        val selfId = sessionHolder.currentUserId
        db.withTransaction {
            for (body in bodies) {
                if (body.chatId.isEmpty()) continue
                // The frame names a chat id and nothing else: no type, no title, no
                // membership. An unknown chat is therefore recorded as UNKNOWN and
                // labelled later from who writes in it (see ChatListRow).
                chats.upsertKnown(chatId = body.chatId, type = TYPE_UNKNOWN, createdAt = body.timestamp)

                val existing = messages.findById(body.messageId)
                // Never downgrade a status: a message already known to be read must not
                // fall back to "sent" because a backfill re-delivered it.
                val status = when {
                    existing?.status == MessageStatuses.READ -> MessageStatuses.READ
                    else -> MessageStatuses.SENT
                }
                val entity = body.toEntity(status).copy(dedupKey = existing?.dedupKey)
                messages.upsert(entity)

                if (bumpChat) {
                    chats.updateLastMessage(
                        chatId = body.chatId,
                        text = previewOf(entity),
                        senderId = body.senderId,
                        seq = body.chatSeq,
                        timestamp = body.timestamp,
                    )
                }
                // Our own messages are read by definition; keep the read cursor from
                // lagging behind our own sends, which would show them as unread.
                if (body.senderId == selfId) {
                    chats.advanceReadCursor(body.chatId, body.chatSeq)
                }
            }
            bodies.map { it.chatId }.distinct().forEach { refreshHistoryFloor(it) }
        }
    }

    /**
     * Resolves a send we are waiting on.
     *
     * The ack carries the server's message id, its per-chat seq and — crucially for
     * a first message to `"@username"` — the id of the chat the gateway resolved or
     * created. That id may be the first time this device learns the chat exists.
     */
    suspend fun ingestAck(ack: SendAck, localId: String, targetRef: String, peerUsername: String?) {
        val selfId = sessionHolder.currentUserId
        db.withTransaction {
            if (targetRef != ack.chatId) {
                // The send was addressed to "@username"; everything filed under that
                // placeholder now belongs to a real chat.
                promotePlaceholder(placeholderId = targetRef, realChatId = ack.chatId, peerUsername = peerUsername)
            }
            chats.upsertKnown(
                chatId = ack.chatId,
                type = if (peerUsername != null) TYPE_DIRECT else TYPE_UNKNOWN,
                peerUsername = peerUsername,
                createdAt = ack.timestamp,
            )

            val local = messages.findById(localId)
            val confirmed = MessageEntity(
                messageId = ack.messageId,
                chatId = ack.chatId,
                senderId = selfId,
                seq = ack.chatSeq,
                text = local?.text.orEmpty(),
                mediaRef = local?.mediaRef,
                attachmentJson = local?.attachmentJson,
                replyTo = local?.replyTo,
                expiresAt = local?.expiresAt ?: 0,
                createdAt = ack.timestamp.takeIf { it > 0 } ?: local?.createdAt ?: 0,
                status = MessageStatuses.SENT,
                dedupKey = ack.dedupKey,
            )
            // One transaction: the local row disappears exactly as the confirmed one
            // appears, so the transcript never shows the message twice.
            messages.replaceLocal(localId, confirmed)
            outbox.remove(ack.dedupKey)
            chats.updateLastMessage(
                chatId = ack.chatId,
                text = previewOf(confirmed),
                senderId = selfId,
                seq = ack.chatSeq,
                timestamp = confirmed.createdAt,
            )
            chats.advanceReadCursor(ack.chatId, ack.chatSeq)
            refreshHistoryFloor(ack.chatId)
        }
    }

    /**
     * Another member's read cursor moved.
     *
     * Only the cursor is stored, never a per-message flag: read state is a single
     * monotonic position, and deriving "this message was read" from it means a
     * message backfilled *after* the receipt arrived is still shown as read — which a
     * one-off UPDATE over the rows that existed at the time would have missed.
     */
    suspend fun ingestReceipt(update: ReadUpdate) {
        if (update.chatId.isEmpty() || update.userId.isEmpty()) return
        val selfId = sessionHolder.currentUserId
        db.withTransaction {
            receipts.upsert(
                ReadReceiptEntity(
                    chatId = update.chatId,
                    userId = update.userId,
                    upToSeq = update.upToChatSeq,
                    updatedAt = System.currentTimeMillis(),
                ),
            )
            // Our own cursor, moved by another device of ours.
            if (update.userId == selfId) {
                chats.advanceReadCursor(update.chatId, update.upToChatSeq)
            }
        }
    }

    /**
     * Moves everything filed under a `"@username"` placeholder onto the real chat.
     *
     * A direct chat does not exist server-side until the first message resolves it,
     * so a conversation can be started — and composed offline — before it has an id.
     * The placeholder is how that works; this is where it is paid off.
     */
    suspend fun promotePlaceholder(placeholderId: String, realChatId: String, peerUsername: String?) {
        if (placeholderId == realChatId) return
        db.withTransaction {
            val placeholder = chats.findById(placeholderId)
            if (placeholder != null && chats.findById(realChatId) == null) {
                chats.insertIgnore(
                    placeholder.copy(
                        chatId = realChatId,
                        type = TYPE_DIRECT,
                        peerUsername = peerUsername ?: placeholder.peerUsername,
                    ),
                )
            }
            messages.moveChat(placeholderId, realChatId)
            outbox.retarget(placeholderId, realChatId)
            chats.deleteById(placeholderId)
        }
    }

    /** Recomputes the "how far back do we hold" floor after new rows landed. */
    private suspend fun refreshHistoryFloor(chatId: String) {
        val chat = chats.findById(chatId) ?: return
        val oldest = messages.oldestSeq(chatId) ?: return
        if (chat.oldestLoadedSeq == 0L || oldest < chat.oldestLoadedSeq) {
            chats.updateHistoryCursor(chatId, oldest, chat.hasMoreHistory)
        }
    }

    private fun previewOf(message: MessageEntity): String = when {
        message.deleted -> ""
        message.text.isNotEmpty() -> message.text
        message.attachmentJson != null || message.mediaRef != null -> ATTACHMENT_PREVIEW
        else -> ""
    }

    companion object {
        const val TYPE_DIRECT = "direct"
        const val TYPE_UNKNOWN = "unknown"

        /**
         * A marker, not a user-visible string: the chat list localises it. Kept out of
         * resources because the data layer has no Context and should not gain one.
         */
        const val ATTACHMENT_PREVIEW = "attachment"
    }
}
