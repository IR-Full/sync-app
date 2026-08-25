package com.syncapp.messenger.di

import android.content.Context
import androidx.room.Room
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

@Module
@InstallIn(SingletonComponent::class)
object DatabaseModule {

    @Provides
    @Singleton
    fun provideDatabase(@ApplicationContext context: Context): syncappDatabase =
        Room.databaseBuilder(context, syncappDatabase::class.java, syncappDatabase.NAME)
            // No fallbackToDestructiveMigration: the outbox lives here, so dropping the
            // database on a schema change would discard messages the user believes they
            // sent. Version bumps ship a migration instead.
            .addMigrations(syncappDatabase.MIGRATION_1_2, syncappDatabase.MIGRATION_2_3)
            .build()

    @Provides
    fun provideChatDao(database: syncappDatabase): ChatDao = database.chatDao()

    @Provides
    fun provideMessageDao(database: syncappDatabase): MessageDao = database.messageDao()

    @Provides
    fun provideOutboxDao(database: syncappDatabase): OutboxDao = database.outboxDao()

    @Provides
    fun provideReadReceiptDao(database: syncappDatabase): ReadReceiptDao = database.readReceiptDao()

    @Provides
    fun provideUserDao(database: syncappDatabase): UserDao = database.userDao()
}
