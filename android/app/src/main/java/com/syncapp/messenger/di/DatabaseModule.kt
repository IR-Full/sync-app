package com.syncapp.messenger.di

import android.content.Context
import android.util.Log
import androidx.room.Room
import com.syncapp.messenger.database.DatabaseEncryption
import com.syncapp.messenger.database.DatabaseKey
import com.syncapp.messenger.database.SyncAppDatabase
import com.syncapp.messenger.database.dao.ChatDao
import com.syncapp.messenger.database.dao.MessageDao
import com.syncapp.messenger.database.dao.OutboxDao
import com.syncapp.messenger.database.dao.ReadReceiptDao
import com.syncapp.messenger.database.dao.UserDao
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton
import net.zetetic.database.sqlcipher.SupportOpenHelperFactory

@Module
@InstallIn(SingletonComponent::class)
object DatabaseModule {

    private const val TAG = "DatabaseModule"

    @Provides
    @Singleton
    fun provideDatabase(
        @ApplicationContext context: Context,
        databaseKey: DatabaseKey,
    ): SyncAppDatabase {
        val passphrase = databaseKey.passphrase() ?: recover(context, databaseKey)
        DatabaseEncryption.encryptPlaintextDatabase(context, SyncAppDatabase.NAME, passphrase)

        return Room.databaseBuilder(context, SyncAppDatabase::class.java, SyncAppDatabase.NAME)
            // The whole message history lives in this file, which app-private
            // storage protects only on a device nobody has root on. SQLCipher
            // makes the file itself unreadable without a key held in the
            // keystore — see DatabaseKey for what that does and does not buy.
            .openHelperFactory(SupportOpenHelperFactory(passphrase))
            // No fallbackToDestructiveMigration: the outbox lives here, so dropping the
            // database on a schema change would discard messages the user believes they
            // sent. Version bumps ship a migration instead.
            .addMigrations(
                SyncAppDatabase.MIGRATION_1_2,
                SyncAppDatabase.MIGRATION_2_3,
                SyncAppDatabase.MIGRATION_3_4,
            )
            .build()
    }

    /**
     * Starts over when the stored passphrase cannot be unwrapped.
     *
     * The database it opened is unreadable by anyone, this app included, so the
     * only question is whether the app starts. It does: the history re-syncs
     * from the server, and refusing to launch would leave the user with no way
     * out at all. The outbox is the real loss, which is why this path is meant
     * to be unreachable — the app sets `allowBackup="false"` precisely so a
     * restore never lands a database here without its keystore key.
     */
    private fun recover(context: Context, databaseKey: DatabaseKey): ByteArray {
        Log.e(TAG, "database key lost; discarding the local database")
        context.deleteDatabase(SyncAppDatabase.NAME)
        databaseKey.forget()
        return checkNotNull(databaseKey.passphrase()) { "no database key after reset" }
    }

    @Provides
    fun provideChatDao(database: SyncAppDatabase): ChatDao = database.chatDao()

    @Provides
    fun provideMessageDao(database: SyncAppDatabase): MessageDao = database.messageDao()

    @Provides
    fun provideOutboxDao(database: SyncAppDatabase): OutboxDao = database.outboxDao()

    @Provides
    fun provideReadReceiptDao(database: SyncAppDatabase): ReadReceiptDao = database.readReceiptDao()

    @Provides
    fun provideUserDao(database: SyncAppDatabase): UserDao = database.userDao()
}
