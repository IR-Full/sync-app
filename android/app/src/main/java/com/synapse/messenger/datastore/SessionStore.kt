package com.synapse.messenger.datastore

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import com.synapse.messenger.di.SessionPreferences
import com.synapse.messenger.network.DeviceIdProvider
import com.synapse.messenger.network.GatewaySession
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
 */
@Singleton
class SessionStore @Inject constructor(
    @param:SessionPreferences private val store: DataStore<Preferences>,
) : DeviceIdProvider {

    data class StoredSession(
        val userId: String,
        val username: String,
        val deviceId: String,
        val sessionId: String,
        val token: String,
        val resumeToken: String,
    )

    val session: Flow<StoredSession?> = store.data.map { prefs ->
        val userId = prefs[KEY_USER_ID]
        val token = prefs[KEY_TOKEN]
        if (userId.isNullOrEmpty() || token.isNullOrEmpty()) return@map null
        StoredSession(
            userId = userId,
            username = prefs[KEY_USERNAME].orEmpty(),
            deviceId = prefs[KEY_DEVICE_ID].orEmpty(),
            sessionId = prefs[KEY_SESSION_ID].orEmpty(),
            token = token,
            resumeToken = prefs[KEY_RESUME_TOKEN].orEmpty(),
        )
    }

    suspend fun current(): StoredSession? = session.first()

    suspend fun save(gateway: GatewaySession, username: String?) {
        store.edit { prefs ->
            prefs[KEY_USER_ID] = gateway.userId
            prefs[KEY_SESSION_ID] = gateway.sessionId
            prefs[KEY_TOKEN] = gateway.token
            prefs[KEY_RESUME_TOKEN] = gateway.resumeToken
            if (gateway.deviceId.isNotEmpty()) prefs[KEY_DEVICE_ID] = gateway.deviceId
            if (!username.isNullOrEmpty()) prefs[KEY_USERNAME] = username.lowercase()
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
    }
}
