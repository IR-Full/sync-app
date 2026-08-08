package com.synapse.messenger.database.dao

import androidx.room.Dao
import androidx.room.Embedded
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.Transaction
import com.synapse.messenger.database.entity.ChatEntity
import kotlinx.coroutines.flow.Flow

/**
 * A chat plus the facts that are derived, not stored.
 *
 * [otherSenderId] and [otherSenderCount] exist because a NEW frame names a chat
 * id and nothing else: the protocol never tells a client a chat's type, title or
 * membership. So for a conversation this device learned about by receiving a
 * message, the only available label is "who has written in it" — one other sender
 * means it renders as a direct chat with that person, several mean it is a group
 * we have no title for.
 */
data class ChatListRow(
    @Embedded val chat: ChatEntity,
    val unreadCount: Int,
    val otherSenderId: String?,
    val otherSenderCount: Int,
)

@Dao
interface ChatDao {

    /**
     * The chat list. Unread is counted from the messages themselves against our
     * read cursor, and excludes our own messages and tombstones — a stored counter
     * would drift the first time a message arrived twice (fanout plus history
     * backfill both land here, idempotently).
     */
    @Query(
        """
        SELECT c.*, (
            SELECT COUNT(*) FROM messages m
            WHERE m.chatId = c.chatId
              AND m.seq > c.myReadSeq
              AND m.senderId != :selfId
              AND m.deleted = 0
        ) AS unreadCount, (
            SELECT m.senderId FROM messages m
            WHERE m.chatId = c.chatId AND m.senderId != :selfId
            ORDER BY m.seq ASC LIMIT 1
        ) AS otherSenderId, (
            SELECT COUNT(DISTINCT m.senderId) FROM messages m
            WHERE m.chatId = c.chatId AND m.senderId != :selfId
        ) AS otherSenderCount
        FROM chats c
        ORDER BY CASE WHEN c.lastMessageAt > 0 THEN c.lastMessageAt ELSE c.createdAt END DESC
        """,
    )
    fun observeChatList(selfId: String): Flow<List<ChatListRow>>

    @Query("SELECT * FROM chats WHERE chatId = :chatId")
    fun observeChat(chatId: String): Flow<ChatEntity?>

    @Query("SELECT * FROM chats WHERE chatId = :chatId")
    suspend fun findById(chatId: String): ChatEntity?

    @Query("SELECT * FROM chats WHERE peerUsername = :username COLLATE NOCASE LIMIT 1")
    suspend fun findDirectByUsername(username: String): ChatEntity?

    /**
     * The direct chat with a given person, by their user id — which is how a
     * chat-list row identifies the other side of a 1:1 conversation.
     */
    @Query(
        """
        SELECT * FROM chats
        WHERE peerUserId = :peerUserId AND chatId NOT LIKE '@%'
        LIMIT 1
        """,
    )
    suspend fun findDirectByPeer(peerUserId: String): ChatEntity?

    /**
     * The real id of a direct chat, once it has one.
     *
     * The `NOT LIKE '@%'` excludes the placeholder row a conversation lives in
     * before its first message has been acknowledged: a chat screen opened on a
     * stranger watches this to learn the id the gateway assigned.
     */
    @Query(
        """
        SELECT chatId FROM chats
        WHERE peerUsername = :username COLLATE NOCASE AND chatId NOT LIKE '@%'
        LIMIT 1
        """,
    )
    fun observeResolvedDirectChatId(username: String): Flow<String?>

    @Query("SELECT chatId FROM chats")
    suspend fun allChatIds(): List<String>

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertIgnore(chat: ChatEntity): Long

    @Query("UPDATE chats SET myReadSeq = :seq WHERE chatId = :chatId AND myReadSeq < :seq")
    suspend fun advanceReadCursor(chatId: String, seq: Long)

    @Query(
        """
        UPDATE chats SET
            lastMessageText = :text,
            lastMessageSenderId = :senderId,
            lastMessageSeq = :seq,
            lastMessageAt = :timestamp
        WHERE chatId = :chatId AND lastMessageSeq <= :seq
        """,
    )
    suspend fun updateLastMessage(
        chatId: String,
        text: String?,
        senderId: String?,
        seq: Long,
        timestamp: Long,
    )

    @Query("UPDATE chats SET title = :title WHERE chatId = :chatId")
    suspend fun updateTitle(chatId: String, title: String)

    @Query("UPDATE chats SET peerUserId = :userId, peerUsername = :username WHERE chatId = :chatId")
    suspend fun updatePeer(chatId: String, userId: String?, username: String?)

    @Query("UPDATE chats SET oldestLoadedSeq = :oldest, hasMoreHistory = :hasMore WHERE chatId = :chatId")
    suspend fun updateHistoryCursor(chatId: String, oldest: Long, hasMore: Boolean)

    /**
     * Applies a CHAT_LIST row. Unlike [upsertKnown] this overwrites: the server's
     * chat list is authoritative about a chat's type, title and owner, and a row
     * first learned from an incoming message holds guesses for all three.
     */
    @Query(
        """
        UPDATE chats SET
            type = :type,
            title = :title,
            ownerId = :ownerId,
            peerUserId = COALESCE(:peerUserId, peerUserId)
        WHERE chatId = :chatId
        """,
    )
    suspend fun applySummary(
        chatId: String,
        type: String,
        title: String,
        ownerId: String?,
        peerUserId: String?,
    )

    /**
     * Records the handle of a direct chat's peer, learned from their profile. This is
     * what lets "message @bob" find the conversation that already exists instead of
     * probing the server for it.
     */
    @Query("UPDATE chats SET peerUsername = :username WHERE peerUserId = :peerUserId AND peerUsername IS NULL")
    suspend fun setPeerUsername(peerUserId: String, username: String)

    @Query("DELETE FROM chats WHERE chatId = :chatId")
    suspend fun deleteById(chatId: String)

    @Query("DELETE FROM chats")
    suspend fun clear()

    /**
     * Records a chat we just learned about without clobbering what we already
     * know. Fields arrive from different frames — a NEW frame knows the id but not
     * the title, a CHAT_INFO knows the title, a resolved `"@username"` knows the
     * peer — so each writer fills only its own gaps.
     */
    @Transaction
    suspend fun upsertKnown(
        chatId: String,
        type: String,
        title: String? = null,
        peerUserId: String? = null,
        peerUsername: String? = null,
        ownerId: String? = null,
        createdAt: Long = System.currentTimeMillis(),
    ) {
        val existing = findById(chatId)
        if (existing == null) {
            insertIgnore(
                ChatEntity(
                    chatId = chatId,
                    type = type,
                    title = title.orEmpty(),
                    peerUserId = peerUserId,
                    peerUsername = peerUsername,
                    ownerId = ownerId,
                    createdAt = createdAt,
                ),
            )
            return
        }
        if (!title.isNullOrEmpty() && existing.title != title) updateTitle(chatId, title)
        if ((peerUserId != null && existing.peerUserId == null) ||
            (peerUsername != null && existing.peerUsername == null)
        ) {
            updatePeer(
                chatId = chatId,
                userId = peerUserId ?: existing.peerUserId,
                username = peerUsername ?: existing.peerUsername,
            )
        }
    }
}
