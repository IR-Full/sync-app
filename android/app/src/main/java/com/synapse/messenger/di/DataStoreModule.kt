package com.synapse.messenger.di

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.preferencesDataStoreFile
import com.synapse.messenger.core.AppScope
import com.synapse.messenger.datastore.SessionStore
import com.synapse.messenger.datastore.SettingsStore
import com.synapse.messenger.network.DeviceIdProvider
import com.synapse.messenger.network.GatewayEndpointProvider
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Qualifier
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope

@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class SessionPreferences

@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class SettingsPreferences

/**
 * Two preference files, not one.
 *
 * Session state is wiped on logout and settings are not — separate files make that
 * a file-scoped operation instead of a careful list of keys to keep, which is the
 * kind of list that eventually forgets one.
 */
@Module
@InstallIn(SingletonComponent::class)
object DataStoreModule {

    @Provides
    @Singleton
    @SessionPreferences
    fun provideSessionPreferences(
        @ApplicationContext context: Context,
        @AppScope scope: CoroutineScope,
    ): DataStore<Preferences> = PreferenceDataStoreFactory.create(scope = scope) {
        context.preferencesDataStoreFile("session")
    }

    @Provides
    @Singleton
    @SettingsPreferences
    fun provideSettingsPreferences(
        @ApplicationContext context: Context,
        @AppScope scope: CoroutineScope,
    ): DataStore<Preferences> = PreferenceDataStoreFactory.create(scope = scope) {
        context.preferencesDataStoreFile("settings")
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class StoreBindingsModule {

    @Binds
    abstract fun bindEndpointProvider(store: SettingsStore): GatewayEndpointProvider

    @Binds
    abstract fun bindDeviceIdProvider(store: SessionStore): DeviceIdProvider
}
