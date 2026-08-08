package com.synapse.messenger.di

import android.content.Context
import androidx.room.Room
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.database.dao.ChatDao
import com.synapse.messenger.database.dao.MessageDao
import com.synapse.messenger.database.dao.OutboxDao
import com.synapse.messenger.database.dao.ReadReceiptDao
import com.synapse.messenger.database.dao.UserDao
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
    fun provideDatabase(@ApplicationContext context: Context): SynapseDatabase =
        Room.databaseBuilder(context, SynapseDatabase::class.java, SynapseDatabase.NAME)
            // No fallbackToDestructiveMigration: this database holds the only copy of
            // the chat list (the protocol cannot enumerate chats), so dropping it on a
            // schema change would lose conversations, not just a cache. Version bumps
            // must ship a migration.
            .build()

    @Provides
    fun provideChatDao(database: SynapseDatabase): ChatDao = database.chatDao()

    @Provides
    fun provideMessageDao(database: SynapseDatabase): MessageDao = database.messageDao()

    @Provides
    fun provideOutboxDao(database: SynapseDatabase): OutboxDao = database.outboxDao()

    @Provides
    fun provideReadReceiptDao(database: SynapseDatabase): ReadReceiptDao = database.readReceiptDao()

    @Provides
    fun provideUserDao(database: SynapseDatabase): UserDao = database.userDao()
}
