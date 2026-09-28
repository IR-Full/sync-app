package com.syncapp.messenger.database

import androidx.room.Database
import androidx.room.RoomDatabase
import androidx.room.migration.Migration
import androidx.room.withTransaction
import androidx.sqlite.SQLiteConnection
import androidx.sqlite.execSQL
import com.syncapp.messenger.database.dao.ChatDao
import com.syncapp.messenger.database.dao.MessageDao
import com.syncapp.messenger.database.dao.OutboxDao
import com.syncapp.messenger.database.dao.ReadReceiptDao
import com.syncapp.messenger.database.dao.UserDao
import com.syncapp.messenger.database.entity.ChatEntity
import com.syncapp.messenger.database.entity.DeliveryReceiptEntity
import com.syncapp.messenger.database.entity.MessageEntity
import com.syncapp.messenger.database.entity.OutboxEntity
import com.syncapp.messenger.database.entity.ReadReceiptEntity
import com.syncapp.messenger.database.entity.UserEntity

@Database(
    entities = [
        ChatEntity::class,
        MessageEntity::class,
        OutboxEntity::class,
        ReadReceiptEntity::class,
        DeliveryReceiptEntity::class,
        UserEntity::class,
    ],
    version = 4,
    exportSchema = true,
)
abstract class SyncAppDatabase : RoomDatabase() {
    abstract fun chatDao(): ChatDao
    abstract fun messageDao(): MessageDao
    abstract fun outboxDao(): OutboxDao
    abstract fun readReceiptDao(): ReadReceiptDao
    abstract fun userDao(): UserDao

    companion object {
        const val NAME = "syncapp.db"

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
        /**
         * Per-member chat flags come back, and the ordering key with them.
         *
         * `muted` was dropped in migration 2 because nothing could set it. It returns as
         * `mutedUntil`, a deadline, now that CHAT_FLAGS exists — and alongside the two
         * other settings the server keeps per member, plus the activity timestamp the
         * chat list is actually ordered by.
         *
         * `ALTER TABLE ADD COLUMN` rather than a table rebuild: these are additions with
         * defaults, and a rebuild is the operation that can fail on a user's phone.
         */
        val MIGRATION_3_4 = object : Migration(3, 4) {
            override fun migrate(connection: SQLiteConnection) {
                connection.execSQL("ALTER TABLE chats ADD COLUMN mutedUntil INTEGER NOT NULL DEFAULT 0")
                connection.execSQL("ALTER TABLE chats ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0")
                connection.execSQL("ALTER TABLE chats ADD COLUMN archived INTEGER NOT NULL DEFAULT 0")
                connection.execSQL(
                    "ALTER TABLE chats ADD COLUMN lastActivityAt INTEGER NOT NULL DEFAULT 0",
                )
                // Seed it from what the rows already hold, so the first list render after
                // an upgrade is ordered rather than arbitrary — the next chat-list
                // enumeration replaces these with the server's own values.
                connection.execSQL(
                    "UPDATE chats SET lastActivityAt = MAX(lastMessageAt, createdAt)",
                )
            }
        }

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
suspend fun SyncAppDatabase.clearUserData() = withTransaction {
    messageDao().clear()
    outboxDao().clear()
    readReceiptDao().clear()
    readReceiptDao().clearDelivery()
    chatDao().clear()
    userDao().clear()
}
