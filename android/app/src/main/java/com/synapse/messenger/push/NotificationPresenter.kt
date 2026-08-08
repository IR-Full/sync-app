package com.synapse.messenger.push

import android.Manifest
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.core.content.getSystemService
import com.synapse.messenger.MainActivity
import com.synapse.messenger.R
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
) {
    fun show(chatId: String, messageId: String, title: String, body: String) {
        if (!hasPermission()) return
        val manager = context.getSystemService<NotificationManager>() ?: return

        val intent = Intent(
            Intent.ACTION_VIEW,
            Uri.parse("$DEEP_LINK_SCHEME://chat/$chatId"),
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
        const val DEEP_LINK_SCHEME = "synapse"
    }
}
