package com.syncapp.messenger.push

import android.Manifest
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.net.toUri
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.core.content.getSystemService
import com.syncapp.messenger.MainActivity
import com.syncapp.messenger.R
import com.syncapp.messenger.data.ActiveChatTracker
import com.syncapp.messenger.database.dao.MessageDao
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Raises a message notification that opens the right chat.
 *
 * The deep link is the whole point of the payload the server sends: `notify` posts
 * `{title, body, chat_id, message_id}`, so a tap can land in the conversation
 * instead of the chat list. The message id is used as the notification id so the
 * at-least-once push path cannot stack the same message twice.
 */
@Singleton
class NotificationPresenter @Inject constructor(
    private val context: Context,
    private val activeChat: ActiveChatTracker,
    private val messages: MessageDao,
) {
    suspend fun show(chatId: String, messageId: String, title: String, body: String) {
        if (!hasPermission()) return
        if (shouldSuppress(chatId, messageId)) return
        val manager = context.getSystemService<NotificationManager>() ?: return

        val intent = Intent(
            Intent.ACTION_VIEW,
            "$DEEP_LINK_SCHEME://chat/$chatId".toUri(),
            context,
            MainActivity::class.java,
        ).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val pending = PendingIntent.getActivity(
            context,
            chatId.hashCode(),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

        val notification = NotificationCompat.Builder(context, CHANNEL_MESSAGES)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title.ifEmpty { context.getString(R.string.notification_default_title) })
            .setContentText(body)
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setContentIntent(pending)
            .build()

        manager.notify(notificationIdFor(messageId, chatId), notification)
    }

    /**
     * Whether this push has already been answered by the app itself.
     *
     * Two cases, both of which produce a notification for something the user has
     * already got. Push and socket delivery are independent paths — the gateway
     * pushes to devices it does not see connected at that instant, which includes
     * one whose socket reconnected a moment later — so the message frequently
     * lands in the database before (or instead of) the notification arriving:
     *
     *  - the chat is open on screen, so the message is visible as it arrives;
     *  - the message is already stored, so the socket delivered it.
     */
    private suspend fun shouldSuppress(chatId: String, messageId: String): Boolean {
        if (chatId.isNotEmpty() && activeChat.chatId.value == chatId) return true
        if (messageId.isEmpty()) return false
        return messages.findById(messageId) != null
    }

    /** POST_NOTIFICATIONS only exists from API 33; before that, posting is always allowed. */
    private fun hasPermission(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return true
        return ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
    }

    /** Stable per message so a duplicate push replaces rather than stacks. */
    private fun notificationIdFor(messageId: String, chatId: String): Int =
        messageId.takeIf { it.isNotEmpty() }?.hashCode() ?: chatId.hashCode()

    companion object {
        const val CHANNEL_MESSAGES = "messages"
        const val DEEP_LINK_SCHEME = "syncapp"
    }
}
