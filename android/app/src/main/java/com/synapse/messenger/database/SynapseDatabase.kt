package com.synapse.messenger.database

import androidx.room.Database
import androidx.room.RoomDatabase
import androidx.room.migration.Migration
import androidx.room.withTransaction
import androidx.sqlite.SQLiteConnection
import androidx.sqlite.execSQL
import com.synapse.messenger.database.dao.ChatDao
import com.synapse.messenger.database.dao.MessageDao
import com.synapse.messenger.database.dao.OutboxDao
import com.synapse.messenger.database.dao.ReadReceiptDao
import com.synapse.messenger.database.dao.UserDao
import com.synapse.messenger.database.entity.ChatEntity
import com.synapse.messenger.database.entity.DeliveryReceiptEntity
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
        DeliveryReceiptEntity::class,
        UserEntity::class,
    ],
    version = 3,
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

        /**
         * The gateway grew profiles (PROFILE_GET/PROFILE_SET), so a person now has a
         * public name and an avatar reference alongside the private label we gave them.
         *
         * The chats table loses `muted`: per-chat mute has no message behind it, and a
         * column nothing can set is a promise the app cannot keep. Rebuilding the table
         * is the only way SQLite drops a column on the versions this app supports.
         */
        /**
         * The gateway grew delivery receipts (DELIVERED), reported by the node that
         * actually wrote a message to a recipient's socket. A second cursor, kept in
         * its own table: a message can be delivered and never read, so folding the
         * two into one column would make the earlier fact unrepresentable.
         */
        val MIGRATION_2_3 = object : Migration(2, 3) {
            override fun migrate(connection: SQLiteConnection) {
                connection.execSQL(
                    """
                    CREATE TABLE IF NOT EXISTS delivery_receipts (
                        chatId TEXT NOT NULL,
                        userId TEXT NOT NULL,
                        upToSeq INTEGER NOT NULL,
                        updatedAt INTEGER NOT NULL,
                        PRIMARY KEY(chatId, userId)
                    )
                    """.trimIndent(),
                )
            }
        }

        val MIGRATION_1_2 = object : Migration(1, 2) {
            override fun migrate(connection: SQLiteConnection) {
                connection.execSQL("ALTER TABLE users ADD COLUMN displayName TEXT")
                connection.execSQL("ALTER TABLE users ADD COLUMN avatarRef TEXT")

                connection.execSQL(
                    """
                    CREATE TABLE chats_new (
                        chatId TEXT NOT NULL PRIMARY KEY,
                        type TEXT NOT NULL,
                        title TEXT NOT NULL,
                        peerUserId TEXT,
                        peerUsername TEXT,
                        ownerId TEXT,
                        lastMessageText TEXT,
                        lastMessageSenderId TEXT,
                        lastMessageSeq INTEGER NOT NULL,
                        lastMessageAt INTEGER NOT NULL,
                        myReadSeq INTEGER NOT NULL,
                        oldestLoadedSeq INTEGER NOT NULL,
                        hasMoreHistory INTEGER NOT NULL,
                        createdAt INTEGER NOT NULL
                    )
                    """.trimIndent(),
                )
                connection.execSQL(
                    """
                    INSERT INTO chats_new
                    SELECT chatId, type, title, peerUserId, peerUsername, ownerId,
                           lastMessageText, lastMessageSenderId, lastMessageSeq, lastMessageAt,
                           myReadSeq, oldestLoadedSeq, hasMoreHistory, createdAt
                    FROM chats
                    """.trimIndent(),
                )
                connection.execSQL("DROP TABLE chats")
                connection.execSQL("ALTER TABLE chats_new RENAME TO chats")
            }
        }
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
    readReceiptDao().clearDelivery()
    chatDao().clear()
    userDao().clear()
}
