package com.synapse.messenger.database.entity

import androidx.room.Entity
import androidx.room.Index
import androidx.room.PrimaryKey

/**
 * A conversation as this device knows it.
 *
 * The gateway has no "list my chats" message — `store.ListUserChats` exists
 * server-side but is not exposed over the protocol — so this table IS the chat
 * list, not a cache of one. Rows appear when a chat announces itself: a NEW frame,
 * a SEND_ACK resolving `"@username"` to an id, a CHAT_INFO from a create, or an
 * INVITES reply from a join. See README (`Protocol gaps`).
 *
 * [myReadSeq] is our own read cursor, echoed to the server with READ. Unread is
 * derived from it by counting messages instead of being stored, so a stored
 * counter can never drift away from the messages it claims to count.
 */
@Entity(tableName = "chats")
data class ChatEntity(
    @PrimaryKey val chatId: String,
    val type: String,
    val title: String = "",
    /** For direct chats: who we are talking to, once known. */
    val peerUserId: String? = null,
    val peerUsername: String? = null,
    val ownerId: String? = null,
    val lastMessageText: String? = null,
    val lastMessageSenderId: String? = null,
    val lastMessageSeq: Long = 0,
    val lastMessageAt: Long = 0,
    val myReadSeq: Long = 0,
    /** Lowest chat_seq we hold locally, and whether older pages remain on the server. */
    val oldestLoadedSeq: Long = 0,
    val hasMoreHistory: Boolean = true,
    val createdAt: Long = 0,
)

/**
 * One message. [seq] is the server's per-chat ordering position — strictly
 * increasing and gap-free — which is what defines display order. A message still
 * in the outbox has no seq yet, so it sorts last by [createdAt] until its
 * SEND_ACK arrives.
 */
@Entity(
    tableName = "messages",
    indices = [
        Index(value = ["chatId", "seq"]),
        Index(value = ["dedupKey"], unique = true),
    ],
)
data class MessageEntity(
    @PrimaryKey val messageId: String,
    val chatId: String,
    val senderId: String,
    val seq: Long = 0,
    val text: String = "",
    val mediaRef: String? = null,
    val attachmentJson: String? = null,
    val replyTo: String? = null,
    val forwardChatId: String? = null,
    val forwardMessageId: String? = null,
    val forwardSenderId: String? = null,
    val threadRoot: String? = null,
    val replyCount: Int = 0,
    /** Self-destruct deadline in unix millis (0 = never). */
    val expiresAt: Long = 0,
    val edited: Boolean = false,
    val deleted: Boolean = false,
    val createdAt: Long = 0,
    val status: String = MessageStatuses.SENT,
    /**
     * Our idempotency key for messages we sent. The gateway maps
     * (device, dedupKey) → message id, so a retry after a dropped socket resolves
     * to the same message instead of a duplicate.
     */
    val dedupKey: String? = null,
)

/** Stored as text so a new status never needs a schema migration. */
object MessageStatuses {
    /** Written locally, not yet acknowledged by the server. */
    const val PENDING = "pending"

    /** Durably persisted server-side (SEND_ACK), or received from fanout/history. */
    const val SENT = "sent"

    /** Someone else's read cursor has passed this message's seq. */
    const val READ = "read"

    /** The send was rejected in a way a retry cannot fix. */
    const val FAILED = "failed"
}

/**
 * A message waiting to reach the server.
 *
 * [targetRef] is what goes in the SEND body's `chat_id`, which may be a numeric
 * chat id OR `"@username"` — the gateway resolves the latter and creates the
 * direct chat on first message. That is why a message can be composed offline for
 * someone we have never messaged: the chat does not have to exist yet.
 */
@Entity(tableName = "outbox")
data class OutboxEntity(
    @PrimaryKey val dedupKey: String,
    val targetRef: String,
    val text: String = "",
    val mediaRef: String? = null,
    val attachmentJson: String? = null,
    val replyTo: String? = null,
    val ttlSeconds: Int = 0,
    val createdAt: Long = 0,
    val attempts: Int = 0,
    val lastError: String? = null,
)

/**
 * How far another member has read in a chat (READ_UPD). For a direct chat the
 * single other row is exactly the "read" tick on our own messages.
 */
@Entity(tableName = "read_receipts", primaryKeys = ["chatId", "userId"])
data class ReadReceiptEntity(
    val chatId: String,
    val userId: String,
    val upToSeq: Long,
    val updatedAt: Long,
)

/**
 * The local user directory.
 *
 * The protocol has no profile fetch: CONTACT_ADD resolves `"@username"` to a user
 * id and CONTACT_SYNC returns ids plus our own private labels, but a username or
 * display name never comes back from the server. So the username is recorded here
 * at the moment we type it, and it is the only way this client can render
 * anything but a numeric id for another person.
 */
@Entity(tableName = "users", indices = [Index(value = ["username"])])
data class UserEntity(
    @PrimaryKey val userId: String,
    val username: String? = null,
    /** Our private label for this person (contacts are per-owner rows server-side). */
    val name: String? = null,
    val isContact: Boolean = false,
    val blocked: Boolean = false,
    val updatedAt: Long = 0,
)
