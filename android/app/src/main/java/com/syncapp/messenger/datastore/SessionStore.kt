package com.syncapp.messenger.datastore

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import com.syncapp.messenger.di.SessionPreferences
import com.syncapp.messenger.network.DeviceIdProvider
import com.syncapp.messenger.network.GatewaySession
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

/**
 * The session, on disk.
 *
 * Both tokens matter and they are not interchangeable: [StoredSession.token] is
 * the bearer credential a cold start logs in with, while `resumeToken` only buys
 * a replay of frames missed during a drop and is re-minted on every login. Losing
 * the resume token costs a history refetch; losing the bearer token costs the user
 * their password prompt.
 *
 * The device id lives here too but is deliberately NOT cleared on logout: the
 * gateway keys the device row — and therefore this phone's push token — on it.
 *
 * Both tokens are encrypted with a non-extractable AndroidKeyStore key (see
 * [TokenCipher]) before they reach the file. Everything else here — ids, a
 * username, a display name — is not a credential and is stored as-is.
 */
@Singleton
class SessionStore @Inject constructor(
    @param:SessionPreferences private val store: DataStore<Preferences>,
    private val cipher: TokenCipher,
) : DeviceIdProvider {

    data class StoredSession(
        val userId: String,
        val username: String,
        val deviceId: String,
        val sessionId: String,
        val token: String,
        val resumeToken: String,
        val displayName: String,
        val avatarRef: String,
    )

    val session: Flow<StoredSession?> = store.data.map { prefs ->
        val userId = prefs[KEY_USER_ID]
        // Both tokens are encrypted at rest; a value that no longer decrypts (the
        // keystore key was invalidated) comes back empty and reads as logged out,
        // which is the recoverable outcome.
        val token = prefs[KEY_TOKEN]?.let(cipher::decrypt)
        if (userId.isNullOrEmpty() || token.isNullOrEmpty()) return@map null
        StoredSession(
            userId = userId,
            username = prefs[KEY_USERNAME].orEmpty(),
            deviceId = prefs[KEY_DEVICE_ID].orEmpty(),
            sessionId = prefs[KEY_SESSION_ID].orEmpty(),
            token = token,
            resumeToken = prefs[KEY_RESUME_TOKEN]?.let(cipher::decrypt).orEmpty(),
            displayName = prefs[KEY_DISPLAY_NAME].orEmpty(),
            avatarRef = prefs[KEY_AVATAR_REF].orEmpty(),
        )
    }

    suspend fun current(): StoredSession? = session.first()

    suspend fun save(gateway: GatewaySession, username: String?) {
        store.edit { prefs ->
            prefs[KEY_USER_ID] = gateway.userId
            prefs[KEY_SESSION_ID] = gateway.sessionId
            prefs[KEY_TOKEN] = cipher.encrypt(gateway.token)
            prefs[KEY_RESUME_TOKEN] = cipher.encrypt(gateway.resumeToken)
            if (gateway.deviceId.isNotEmpty()) prefs[KEY_DEVICE_ID] = gateway.deviceId
            if (!username.isNullOrEmpty()) prefs[KEY_USERNAME] = username.lowercase()
            prefs[KEY_DISPLAY_NAME] = gateway.displayName
            prefs[KEY_AVATAR_REF] = gateway.avatarRef
        }
    }

    suspend fun clear() {
        store.edit { prefs ->
            val deviceId = prefs[KEY_DEVICE_ID]
            prefs.clear()
            // Keep the installation identity: a new one would strand the device row
            // holding this phone's push token, and the user would silently stop
            // getting notifications after a re-login.
            if (deviceId != null) prefs[KEY_DEVICE_ID] = deviceId
        }
    }

    override suspend fun deviceId(): String {
        store.data.first()[KEY_DEVICE_ID]?.takeIf { it.isNotEmpty() }?.let { return it }
        val generated = UUID.randomUUID().toString()
        var result = generated
        store.edit { prefs ->
            val existing = prefs[KEY_DEVICE_ID]
            if (existing.isNullOrEmpty()) prefs[KEY_DEVICE_ID] = generated else result = existing
        }
        return result
    }

    private companion object {
        val KEY_USER_ID = stringPreferencesKey("user_id")
        val KEY_USERNAME = stringPreferencesKey("username")
        val KEY_DEVICE_ID = stringPreferencesKey("device_id")
        val KEY_SESSION_ID = stringPreferencesKey("session_id")
        val KEY_TOKEN = stringPreferencesKey("token")
        val KEY_RESUME_TOKEN = stringPreferencesKey("resume_token")
        val KEY_DISPLAY_NAME = stringPreferencesKey("display_name")
        val KEY_AVATAR_REF = stringPreferencesKey("avatar_ref")
    }
}
