package com.syncapp.messenger

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import androidx.core.content.getSystemService
import androidx.hilt.work.HiltWorkerFactory
import androidx.work.Configuration
import com.syncapp.messenger.data.sync.BackgroundSyncWorker
import com.syncapp.messenger.data.sync.SyncCoordinator
import com.syncapp.messenger.push.NotificationPresenter
import dagger.hilt.android.HiltAndroidApp
import javax.inject.Inject

@HiltAndroidApp
class SyncAppApplication : Application(), Configuration.Provider {

    /**
     * Injected here rather than started by a screen: the connection, the ingest of
     * incoming messages and the outbox flush must run whether or not any UI is
     * present — a push can bring the process up with no Activity at all.
     */
    @Inject lateinit var syncCoordinator: SyncCoordinator

    /** Lets WorkManager construct `@HiltWorker` workers with their dependencies. */
    @Inject lateinit var workerFactory: HiltWorkerFactory

    override val workManagerConfiguration: Configuration
        get() = Configuration.Builder()
            .setWorkerFactory(workerFactory)
            .build()

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        syncCoordinator.start()
        // The periodic catch-up for when the socket is suspended and push is not
        // configured. Enqueued unconditionally: the worker itself returns early when
        // nobody is signed in, which is cheaper than tying the schedule to a session
        // that can change while the process is not running.
        BackgroundSyncWorker.schedule(this)
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
