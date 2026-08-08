package com.synapse.messenger

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import androidx.core.content.getSystemService
import com.synapse.messenger.data.sync.SyncCoordinator
import com.synapse.messenger.push.NotificationPresenter
import dagger.hilt.android.HiltAndroidApp
import javax.inject.Inject

@HiltAndroidApp
class SynapseApplication : Application() {

    /**
     * Injected here rather than started by a screen: the connection, the ingest of
     * incoming messages and the outbox flush must run whether or not any UI is
     * present — a push can bring the process up with no Activity at all.
     */
    @Inject lateinit var syncCoordinator: SyncCoordinator

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        syncCoordinator.start()
    }

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            NotificationPresenter.CHANNEL_MESSAGES,
            getString(R.string.notification_channel_messages),
            NotificationManager.IMPORTANCE_HIGH,
        ).apply {
            description = getString(R.string.notification_channel_messages_description)
        }
        getSystemService<NotificationManager>()?.createNotificationChannel(channel)
    }
}
