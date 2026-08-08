package com.synapse.messenger.push

import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.synapse.messenger.core.AppScope
import com.synapse.messenger.datastore.SettingsStore
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Receives pushes and turns them into a notification with a deep link.
 *
 * The payload shape is the server's, not FCM's convention: `internal/notify`
 * POSTs `{token, platform, title, body, chat_id, message_id}` to whatever endpoint
 * the deployment configured, so a relay in front of FCM should forward those keys as
 * a **data** message. Data-only messages are what let this client decide whether to
 * show anything at all (a muted chat, notifications turned off) instead of the
 * system posting it unconditionally.
 *
 * Delivery is at-least-once by design on the server side, which is why the
 * presenter keys the notification by message id.
 */
@AndroidEntryPoint
class SynapseMessagingService : FirebaseMessagingService() {

    @Inject lateinit var presenter: NotificationPresenter

    @Inject lateinit var settingsStore: SettingsStore

    @Inject lateinit var pushTokens: PushTokenRegistrar

    @Inject @AppScope lateinit var scope: CoroutineScope

    override fun onMessageReceived(message: RemoteMessage) {
        val data = message.data
        val chatId = data[KEY_CHAT_ID] ?: return
        val messageId = data[KEY_MESSAGE_ID].orEmpty()
        val title = data[KEY_TITLE] ?: message.notification?.title.orEmpty()
        val body = data[KEY_BODY] ?: message.notification?.body.orEmpty()

        scope.launch {
            if (!settingsStore.settings.first().notificationsEnabled) return@launch
            presenter.show(chatId = chatId, messageId = messageId, title = title, body = body)
        }
    }

    /**
     * A rotated token is useless until the gateway has it, and the rotation can
     * happen while the app is not running — so the registration is pushed from here
     * rather than waited for at the next launch.
     */
    override fun onNewToken(token: String) {
        scope.launch { pushTokens.sync() }
    }

    private companion object {
        const val KEY_CHAT_ID = "chat_id"
        const val KEY_MESSAGE_ID = "message_id"
        const val KEY_TITLE = "title"
        const val KEY_BODY = "body"
    }
}
