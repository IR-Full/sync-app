package com.synapse.messenger.database.dao

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import com.synapse.messenger.database.entity.ReadReceiptEntity
import kotlinx.coroutines.flow.Flow

@Dao
interface ReadReceiptDao {

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsert(receipt: ReadReceiptEntity)

    /**
     * How far the chat's other members have read. For a direct chat this is the
     * peer's cursor, which is what turns one tick into two.
     */
    @Query("SELECT COALESCE(MAX(upToSeq), 0) FROM read_receipts WHERE chatId = :chatId AND userId != :selfId")
    fun observeOthersReadSeq(chatId: String, selfId: String): Flow<Long>

    @Query("DELETE FROM read_receipts")
    suspend fun clear()
}
