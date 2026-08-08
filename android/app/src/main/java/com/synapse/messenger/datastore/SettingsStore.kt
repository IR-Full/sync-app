package com.synapse.messenger.datastore

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import com.synapse.messenger.BuildConfig
import com.synapse.messenger.di.SettingsPreferences
import com.synapse.messenger.network.GatewayEndpointProvider
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

enum class ThemeMode { SYSTEM, LIGHT, DARK }

enum class LanguageMode { SYSTEM, RUSSIAN, ENGLISH }

data class AppSettings(
    val theme: ThemeMode = ThemeMode.SYSTEM,
    val language: LanguageMode = LanguageMode.SYSTEM,
    val notificationsEnabled: Boolean = true,
    /** Non-null only in builds that allow it (development/staging). */
    val gatewayUrlOverride: String? = null,
)

/**
 * User settings, and the endpoint override. Nothing about the person lives here:
 * the display name and avatar are published with PROFILE_SET and read back from the
 * user directory, so this device holds no second copy to drift.
 *
 * The override exists because the gateway address is a deployment concern, not a
 * code constant: a development build points at the emulator's host loopback by
 * default but has to be repointable at a real device's LAN address without a
 * rebuild. Production builds refuse the override
 * ([BuildConfig.ALLOW_ENDPOINT_OVERRIDE]) so a stray preference can never send a
 * user's messages somewhere unintended.
 */
@Singleton
class SettingsStore @Inject constructor(
    @param:SettingsPreferences private val store: DataStore<Preferences>,
) : GatewayEndpointProvider {

    val settings: Flow<AppSettings> = store.data.map { prefs ->
        AppSettings(
            theme = prefs[KEY_THEME]?.let { runCatching { ThemeMode.valueOf(it) }.getOrNull() }
                ?: ThemeMode.SYSTEM,
            language = prefs[KEY_LANGUAGE]?.let { runCatching { LanguageMode.valueOf(it) }.getOrNull() }
                ?: LanguageMode.SYSTEM,
            notificationsEnabled = prefs[KEY_NOTIFICATIONS] ?: true,
            gatewayUrlOverride = prefs[KEY_GATEWAY_URL]?.takeIf {
                it.isNotBlank() && BuildConfig.ALLOW_ENDPOINT_OVERRIDE
            },
        )
    }

    suspend fun setTheme(mode: ThemeMode) = store.edit { it[KEY_THEME] = mode.name }

    suspend fun setLanguage(mode: LanguageMode) = store.edit { it[KEY_LANGUAGE] = mode.name }

    suspend fun setNotificationsEnabled(enabled: Boolean) =
        store.edit { it[KEY_NOTIFICATIONS] = enabled }

    suspend fun setGatewayUrl(url: String?) = store.edit { prefs ->
        if (url.isNullOrBlank()) prefs.remove(KEY_GATEWAY_URL) else prefs[KEY_GATEWAY_URL] = url.trim()
    }

    override suspend fun gatewayUrl(): String =
        settings.first().gatewayUrlOverride ?: BuildConfig.GATEWAY_URL

    private companion object {
        val KEY_THEME = stringPreferencesKey("theme")
        val KEY_LANGUAGE = stringPreferencesKey("language")
        val KEY_NOTIFICATIONS = booleanPreferencesKey("notifications_enabled")
        val KEY_GATEWAY_URL = stringPreferencesKey("gateway_url")
    }
}
