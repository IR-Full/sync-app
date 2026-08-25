package com.syncapp.messenger.di

import com.syncapp.messenger.data.repository.AuthRepositoryImpl
import com.syncapp.messenger.data.repository.ChatRepositoryImpl
import com.syncapp.messenger.data.repository.MediaRepositoryImpl
import com.syncapp.messenger.data.repository.MessageRepositoryImpl
import com.syncapp.messenger.data.repository.UserRepositoryImpl
import com.syncapp.messenger.domain.repository.AuthRepository
import com.syncapp.messenger.domain.repository.ChatRepository
import com.syncapp.messenger.domain.repository.MediaRepository
import com.syncapp.messenger.domain.repository.MessageRepository
import com.syncapp.messenger.domain.repository.UserRepository
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

@Module
@InstallIn(SingletonComponent::class)
abstract class RepositoryModule {

    @Binds
    abstract fun bindAuthRepository(impl: AuthRepositoryImpl): AuthRepository

    @Binds
    abstract fun bindChatRepository(impl: ChatRepositoryImpl): ChatRepository

    @Binds
    abstract fun bindMessageRepository(impl: MessageRepositoryImpl): MessageRepository

    @Binds
    abstract fun bindUserRepository(impl: UserRepositoryImpl): UserRepository

    @Binds
    abstract fun bindMediaRepository(impl: MediaRepositoryImpl): MediaRepository
}
