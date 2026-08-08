package com.synapse.messenger.database.dao

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import com.synapse.messenger.database.entity.OutboxEntity

@Dao
interface OutboxDao {

    @Query("SELECT * FROM outbox ORDER BY createdAt ASC")
    suspend fun pending(): List<OutboxEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun enqueue(entry: OutboxEntity)

    @Query("DELETE FROM outbox WHERE dedupKey = :dedupKey")
    suspend fun remove(dedupKey: String)

    @Query("UPDATE outbox SET attempts = attempts + 1, lastError = :error WHERE dedupKey = :dedupKey")
    suspend fun recordFailure(dedupKey: String, error: String?)

    /** Rewrites `"@username"` targets once the gateway resolved them to a chat id. */
    @Query("UPDATE outbox SET targetRef = :chatId WHERE targetRef = :oldRef")
    suspend fun retarget(oldRef: String, chatId: String)

    @Query("DELETE FROM outbox")
    suspend fun clear()
}
