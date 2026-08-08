package com.synapse.messenger.database

import androidx.room.Database
import androidx.room.RoomDatabase
import androidx.room.withTransaction
import com.synapse.messenger.database.dao.ChatDao
import com.synapse.messenger.database.dao.MessageDao
import com.synapse.messenger.database.dao.OutboxDao
import com.synapse.messenger.database.dao.ReadReceiptDao
import com.synapse.messenger.database.dao.UserDao
import com.synapse.messenger.database.entity.ChatEntity
import com.synapse.messenger.database.entity.MessageEntity
import com.synapse.messenger.database.entity.OutboxEntity
import com.synapse.messenger.database.entity.ReadReceiptEntity
import com.synapse.messenger.database.entity.UserEntity

@Database(
    entities = [
        ChatEntity::class,
        MessageEntity::class,
        OutboxEntity::class,
        ReadReceiptEntity::class,
        UserEntity::class,
    ],
    version = 1,
    exportSchema = true,
)
abstract class SynapseDatabase : RoomDatabase() {
    abstract fun chatDao(): ChatDao
    abstract fun messageDao(): MessageDao
    abstract fun outboxDao(): OutboxDao
    abstract fun readReceiptDao(): ReadReceiptDao
    abstract fun userDao(): UserDao

    companion object {
        const val NAME = "synapse.db"
    }
}

/**
 * Wipes every cached row on logout.
 *
 * This is not housekeeping: the cache is keyed by nothing but the account that
 * filled it, so leaving it behind would show the previous user's chats to the next
 * one on the same phone.
 */
suspend fun SynapseDatabase.clearUserData() = withTransaction {
    messageDao().clear()
    outboxDao().clear()
    readReceiptDao().clear()
    chatDao().clear()
    userDao().clear()
}
