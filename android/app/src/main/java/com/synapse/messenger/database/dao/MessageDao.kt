package com.synapse.messenger.database.dao

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.Transaction
import com.synapse.messenger.database.entity.MessageEntity
import com.synapse.messenger.database.entity.MessageStatuses
import kotlinx.coroutines.flow.Flow

@Dao
interface MessageDao {

    /**
     * The chat transcript, oldest first.
     *
     * Messages still in the outbox have `seq = 0` and must render at the bottom,
     * not the top — hence the CASE: unsent messages sort after every sequenced one,
     * then among themselves by creation time.
     */
    @Query(
        """
        SELECT * FROM messages
        WHERE chatId = :chatId
        ORDER BY CASE WHEN seq = 0 THEN 1 ELSE 0 END, seq ASC, createdAt ASC
        """,
    )
    fun observeMessages(chatId: String): Flow<List<MessageEntity>>

    @Query("SELECT * FROM messages WHERE messageId = :messageId")
    suspend fun findById(messageId: String): MessageEntity?

    @Query("SELECT MIN(seq) FROM messages WHERE chatId = :chatId AND seq > 0")
    suspend fun oldestSeq(chatId: String): Long?

    /** Compared against the chat list's `last_seq` to decide what needs backfilling. */
    @Query("SELECT MAX(seq) FROM messages WHERE chatId = :chatId")
    suspend fun newestSeq(chatId: String): Long?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsert(message: MessageEntity)

    @Query("DELETE FROM messages WHERE messageId = :messageId")
    suspend fun delete(messageId: String)

    @Query("UPDATE messages SET status = :status WHERE messageId = :messageId")
    suspend fun updateStatus(messageId: String, status: String)

    @Query("DELETE FROM messages WHERE expiresAt > 0 AND expiresAt <= :now")
    suspend fun purgeExpired(now: Long)

    /** Re-files messages when a `"@username"` placeholder chat gains its real id. */
    @Query("UPDATE messages SET chatId = :toChatId WHERE chatId = :fromChatId")
    suspend fun moveChat(fromChatId: String, toChatId: String)

    @Query("DELETE FROM messages")
    suspend fun clear()

    /**
     * Replaces a locally-composed message with its server-assigned identity.
     *
     * A pending message is keyed by our dedupKey; the ack brings the real message
     * id, seq and timestamp. Both rows cannot coexist — the transcript would show
     * the message twice — so the local row is dropped in the same transaction that
     * writes the real one.
     */
    @Transaction
    suspend fun replaceLocal(localId: String, confirmed: MessageEntity) {
        delete(localId)
        upsert(confirmed)
    }
}
