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
    /**
     * The name and photo this device shows for itself. Local by necessity: the
     * protocol has no profile write and no avatar concept, so there is nowhere to
     * publish either.
     */
    val displayName: String? = null,
    val avatarPath: String? = null,
)

/**
 * User settings, and the endpoint override.
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
            displayName = prefs[KEY_DISPLAY_NAME]?.takeIf { it.isNotBlank() },
            avatarPath = prefs[KEY_AVATAR_PATH]?.takeIf { it.isNotBlank() },
        )
    }

    suspend fun setTheme(mode: ThemeMode) = store.edit { it[KEY_THEME] = mode.name }

    suspend fun setLanguage(mode: LanguageMode) = store.edit { it[KEY_LANGUAGE] = mode.name }

    suspend fun setNotificationsEnabled(enabled: Boolean) =
        store.edit { it[KEY_NOTIFICATIONS] = enabled }

    suspend fun setGatewayUrl(url: String?) = store.edit { prefs ->
        if (url.isNullOrBlank()) prefs.remove(KEY_GATEWAY_URL) else prefs[KEY_GATEWAY_URL] = url.trim()
    }

    suspend fun setDisplayName(name: String?) = store.edit { prefs ->
        if (name.isNullOrBlank()) prefs.remove(KEY_DISPLAY_NAME) else prefs[KEY_DISPLAY_NAME] = name.trim()
    }

    suspend fun setAvatarPath(path: String?) = store.edit { prefs ->
        if (path.isNullOrBlank()) prefs.remove(KEY_AVATAR_PATH) else prefs[KEY_AVATAR_PATH] = path
    }

    override suspend fun gatewayUrl(): String =
        settings.first().gatewayUrlOverride ?: BuildConfig.GATEWAY_URL

    private companion object {
        val KEY_THEME = stringPreferencesKey("theme")
        val KEY_LANGUAGE = stringPreferencesKey("language")
        val KEY_NOTIFICATIONS = booleanPreferencesKey("notifications_enabled")
        val KEY_GATEWAY_URL = stringPreferencesKey("gateway_url")
        val KEY_DISPLAY_NAME = stringPreferencesKey("display_name")
        val KEY_AVATAR_PATH = stringPreferencesKey("avatar_path")
    }
}
