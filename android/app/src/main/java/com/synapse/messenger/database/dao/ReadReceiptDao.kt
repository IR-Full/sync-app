package com.synapse.messenger.database.dao

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import com.synapse.messenger.database.entity.DeliveryReceiptEntity
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

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsertDelivery(receipt: DeliveryReceiptEntity)

    /**
     * How far the chat's other members have *received*. Behind the read cursor by
     * definition, and the reason a third tick can be drawn honestly: it is reported
     * by the gateway that wrote the bytes to their socket.
     *
     * MAX, so in a group this means "at least one member received it" rather than
     * "all did". That is not a shortcut: the protocol has no member-list message, so
     * this device cannot know who is still missing, and a MIN over the receipts we
     * happen to hold would silently mean "everyone we have heard from" — a number
     * that looks like completeness and is not. For a direct chat, where the tick
     * actually gets read, the two are the same thing.
     */
    @Query("SELECT COALESCE(MAX(upToSeq), 0) FROM delivery_receipts WHERE chatId = :chatId AND userId != :selfId")
    fun observeOthersDeliveredSeq(chatId: String, selfId: String): Flow<Long>

    @Query("DELETE FROM read_receipts")
    suspend fun clear()

    @Query("DELETE FROM delivery_receipts")
    suspend fun clearDelivery()
}
