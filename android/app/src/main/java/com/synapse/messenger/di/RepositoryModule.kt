package com.synapse.messenger.di

import com.synapse.messenger.data.repository.AuthRepositoryImpl
import com.synapse.messenger.data.repository.ChatRepositoryImpl
import com.synapse.messenger.data.repository.MediaRepositoryImpl
import com.synapse.messenger.data.repository.MessageRepositoryImpl
import com.synapse.messenger.data.repository.UserRepositoryImpl
import com.synapse.messenger.domain.repository.AuthRepository
import com.synapse.messenger.domain.repository.ChatRepository
import com.synapse.messenger.domain.repository.MediaRepository
import com.synapse.messenger.domain.repository.MessageRepository
import com.synapse.messenger.domain.repository.UserRepository
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
