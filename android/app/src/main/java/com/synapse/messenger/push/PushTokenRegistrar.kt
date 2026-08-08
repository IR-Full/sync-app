package com.synapse.messenger.push

import android.util.Log
import com.google.firebase.messaging.FirebaseMessaging
import com.synapse.messenger.BuildConfig
import com.synapse.messenger.datastore.SettingsStore
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.MsgType
import com.synapse.messenger.network.protocol.PushToken
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.coroutines.resume
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.suspendCancellableCoroutine

/**
 * Registers (or clears) this device's push token.
 *
 * The protocol side is one message: PUSH_TOKEN, scoped to the authenticated user
 * and *this* connection's device. An empty token clears it, which is how "turn
 * notifications off" is implemented — stopping pushes at the source rather than
 * discarding them on arrival, so a muted phone costs the server nothing.
 *
 * Note what the server does with it: `notify` POSTs `{token, platform, title, body,
 * chat_id, message_id}` to a generic HTTP endpoint (`SYNAPSE_PUSH_ENDPOINT`), not to
 * FCM directly. A deployment needs a small relay in front of FCM for these to
 * arrive. Without one, this call is harmless and inert.
 */
@Singleton
class PushTokenRegistrar @Inject constructor(
    private val gateway: SynapseGateway,
    private val settingsStore: SettingsStore,
) {
    suspend fun sync() {
        val enabled = settingsStore.settings.first().notificationsEnabled
        val token = if (enabled) currentToken() else ""
        if (enabled && token.isEmpty()) return // nothing useful to send yet
        runCatching { gateway.send(MsgType.PUSH_TOKEN, PushToken(token = token)) }
            .onFailure { Log.i(TAG, "push token not registered: ${it.message}") }
    }

    suspend fun clear() {
        runCatching { gateway.send(MsgType.PUSH_TOKEN, PushToken(token = "")) }
    }

    /**
     * Fetches the FCM token, or "" when push is not configured for this build.
     * Firebase throws rather than returning null when `google-services.json` is
     * absent, and an unconfigured build is a normal state here — see the app's
     * `PUSH_ENABLED` build flag.
     */
    private suspend fun currentToken(): String {
        if (!BuildConfig.PUSH_ENABLED) return ""
        return runCatching {
            suspendCancellableCoroutine { continuation ->
                FirebaseMessaging.getInstance().token
                    .addOnCompleteListener { task ->
                        continuation.resume(task.result?.takeIf { task.isSuccessful }.orEmpty())
                    }
            }
        }.getOrElse {
            Log.i(TAG, "FCM unavailable: ${it.message}")
            ""
        }
    }

    private companion object {
        const val TAG = "PushTokenRegistrar"
    }
}
