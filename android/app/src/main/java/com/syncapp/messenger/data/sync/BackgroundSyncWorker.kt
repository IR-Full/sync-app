package com.syncapp.messenger.data.sync

import android.content.Context
import android.util.Log
import androidx.hilt.work.HiltWorker
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.syncapp.messenger.datastore.SessionStore
import com.syncapp.messenger.network.ConnectionState
import com.syncapp.messenger.network.Credentials
import com.syncapp.messenger.network.SyncAppGateway
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withTimeoutOrNull

/**
 * A periodic catch-up, for when neither the socket nor a push is doing its job.
 *
 * The app's live path is a socket held by the process, and its fallback is a push
 * — but both can be absent at once, and routinely are. Android suspends a process
 * without a foreground service well within a Doze window, so the socket is gone
 * while backgrounded; and the server does not talk to FCM directly (`internal/notify`
 * POSTs to whatever `SyncApp_PUSH_ENDPOINT` names, defaulting to a *logger*), so a
 * deployment without that relay pushes nothing at all. With neither, the cache goes
 * stale until the user next opens the app.
 *
 * This closes that: connect, let the ordinary sync path run, disconnect. It is not
 * a replacement for push — WorkManager's floor is 15 minutes and the system may
 * defer it further — it is the safety net that keeps "the app was closed for a day"
 * from meaning "the app opens empty".
 */
@HiltWorker
class BackgroundSyncWorker @AssistedInject constructor(
    @Assisted context: Context,
    @Assisted params: WorkerParameters,
    private val gateway: SyncAppGateway,
    private val sessionStore: SessionStore,
    private val chatListSyncer: ChatListSyncer,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val session = sessionStore.current() ?: return Result.success() // signed out
        // The process may still be alive with a healthy socket; the live path is
        // already doing this work, so there is nothing to add.
        if (gateway.state.value == ConnectionState.READY) return Result.success()

        return try {
            withTimeoutOrNull(WORK_TIMEOUT_MS) {
                gateway.connect(Credentials.Token(session.token))
                // SyncCoordinator reacts to the authentication event and flushes the
                // outbox, syncs contacts and registers the push token. The chat list
                // is the one thing it does not pull, and it is what a stale cache
                // most visibly lacks.
                chatListSyncer.sync()
            } ?: return Result.retry() // ran out of time; try again on the next window

            Result.success()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            Log.i(TAG, "background sync failed: ${e.message}")
            Result.retry()
        } finally {
            // Hand the socket back. Keeping it open would be a connection the system
            // is about to kill anyway, counted against the user's battery.
            // `clearSession = false`: this is not a logout.
            gateway.disconnect(clearSession = false)
        }
    }

    companion object {
        private const val TAG = "BackgroundSync"
        private const val WORK_TIMEOUT_MS = 60_000L
        private const val UNIQUE_NAME = "syncapp.background-sync"

        /**
         * Registers the periodic job. Idempotent — KEEP means an existing schedule
         * survives an app restart rather than being reset on every launch, which
         * would push the next run 15 minutes out every time the user opens the app.
         */
        fun schedule(context: Context) {
            val request = PeriodicWorkRequestBuilder<BackgroundSyncWorker>(15, TimeUnit.MINUTES)
                .setConstraints(
                    Constraints.Builder()
                        .setRequiredNetworkType(NetworkType.CONNECTED)
                        .build(),
                )
                .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                UNIQUE_NAME,
                ExistingPeriodicWorkPolicy.KEEP,
                request,
            )
        }

        /** Stops the job. Called on logout: there is nothing to sync for nobody. */
        fun cancel(context: Context) {
            WorkManager.getInstance(context).cancelUniqueWork(UNIQUE_NAME)
        }
    }
}
