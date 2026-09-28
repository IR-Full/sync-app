package com.syncapp.messenger.di

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.preferencesDataStoreFile
import com.syncapp.messenger.core.AppScope
import com.syncapp.messenger.datastore.SessionStore
import com.syncapp.messenger.datastore.SettingsStore
import com.syncapp.messenger.network.DeviceIdProvider
import com.syncapp.messenger.network.GatewayEndpointProvider
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

@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class SecretPreferences

/**
 * Three preference files, not one.
 *
 * Session state is wiped on logout and settings are not — separate files make that
 * a file-scoped operation instead of a careful list of keys to keep, which is the
 * kind of list that eventually forgets one.
 *
 * Secret-chat material gets its own file for the same reason plus one more: it is
 * the only store here whose contents cannot be re-obtained by logging in again.
 * A session token is a cache of something the server will reissue; a ratchet
 * identity is not, and losing it silently would strand every secret conversation
 * this device has. Keeping it apart makes "wipe the session" and "wipe the keys"
 * two different decisions rather than one careless one.
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

    @Provides
    @Singleton
    @SecretPreferences
    fun provideSecretPreferences(
        @ApplicationContext context: Context,
        @AppScope scope: CoroutineScope,
    ): DataStore<Preferences> = PreferenceDataStoreFactory.create(scope = scope) {
        context.preferencesDataStoreFile("secret-chat")
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
