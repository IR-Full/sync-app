package com.synapse.messenger.data.sync

import android.util.Log
import com.synapse.messenger.core.AppScope
import com.synapse.messenger.data.repository.MessageRepositoryImpl
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.datastore.SessionStore
import com.synapse.messenger.network.ConnectionState
import com.synapse.messenger.network.Credentials
import com.synapse.messenger.network.ServerEvent
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.push.PushTokenRegistrar
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * The one long-lived consumer of the gateway.
 *
 * Every unsolicited frame lands here and becomes a database row, so no ViewModel
 * ever subscribes to the socket: screens observe Room, and Room is fed from exactly
 * one place. That is also what makes an offline start coherent — the UI renders the
 * cache whether or not this coordinator has managed to connect.
 *
 * Started from `Application.onCreate` via [start].
 */
@Singleton
class SyncCoordinator @Inject constructor(
    private val gateway: SynapseGateway,
    private val sessionStore: SessionStore,
    private val database: SynapseDatabase,
    private val ingestor: MessageIngestor,
    private val typingTracker: TypingTracker,
    private val messageRepository: MessageRepositoryImpl,
    private val userSync: ContactSyncer,
    private val pushTokens: PushTokenRegistrar,
    private val networkMonitor: NetworkMonitor,
    @param:AppScope private val scope: CoroutineScope,
) {
    private val connectLock = Mutex()
    private var started = false

    fun start() {
        if (started) return
        started = true

        scope.launch { consumeEvents() }
        scope.launch { watchConnectivity() }
        scope.launch { autoConnect() }
    }

    /** Signs in with the stored bearer token, if there is one. */
    private suspend fun autoConnect() {
        val stored = sessionStore.current() ?: return
        connect(stored.token)
    }

    /** Serialised: two triggers (startup and connectivity) must not race into two sockets. */
    private suspend fun connect(token: String) {
        connectLock.withLock {
            if (gateway.state.value == ConnectionState.READY) return
            runCatching { gateway.connect(Credentials.Token(token)) }
                .onFailure { Log.i(TAG, "auto-connect failed: ${it.message}") }
        }
    }

    /**
     * Collapses the reconnect wait when connectivity returns. The gateway's own
     * backoff would get there eventually; a user who just walked out of a tunnel
     * should not wait out the rest of it.
     */
    private suspend fun watchConnectivity() {
        networkMonitor.online.collect { online ->
            if (!online) return@collect
            val state = gateway.state.value
            if (state == ConnectionState.READY || state == ConnectionState.CONNECTING) return@collect
            val stored = sessionStore.current() ?: return@collect
            connect(stored.token)
        }
    }

    private suspend fun consumeEvents() {
        gateway.events.collect { event ->
            when (event) {
                is ServerEvent.Authenticated -> onAuthenticated(event)
                is ServerEvent.Message -> ingestor.ingest(event.body)
                is ServerEvent.ReadReceipt -> ingestor.ingestReceipt(event.body)
                is ServerEvent.TypingSignal ->
                    typingTracker.onTyping(event.body.chatId, event.body.userId, event.body.active)
                is ServerEvent.ChatCreated -> database.chatDao().upsertKnown(
                    chatId = event.body.chatId,
                    type = event.body.type,
                    title = event.body.title,
                    ownerId = event.body.ownerId,
                )
                // A SEND_ACK is normally the correlated reply to our own SEND and is
                // handled there; one arriving unsolicited is another device's send, whose
                // message reaches us as a NEW frame anyway.
                is ServerEvent.Acked -> Unit
                is ServerEvent.PresenceUpdate -> Unit
                is ServerEvent.Failure -> Log.i(TAG, "gateway error: ${event.error}")
                is ServerEvent.SessionExpired -> onSessionExpired()
            }
        }
    }

    /**
     * The connection just became usable. Order matters: persist the session first
     * (the resume token is re-minted on every login and is what a later drop replays
     * from), then push the queue, then reconcile the slower state.
     */
    private suspend fun onAuthenticated(event: ServerEvent.Authenticated) {
        val username = sessionStore.current()?.username
        sessionStore.save(event.session, username)
        scope.launch { messageRepository.flushOutbox() }
        scope.launch { userSync.syncQuietly() }
        scope.launch { pushTokens.sync() }
        // Self-destructing messages carry a deadline, and the server reaps its own
        // copy — a cached one that outlived it would make this device the only place
        // the message still exists.
        scope.launch { database.messageDao().purgeExpired(System.currentTimeMillis()) }
    }

    /**
     * The session died server-side (revoked, expired). Tokens go, cached rows stay:
     * the same user logging back in should not have to refetch their history, and a
     * *different* user logging in wipes the cache at login (see AuthRepositoryImpl).
     */
    private suspend fun onSessionExpired() {
        Log.i(TAG, "session expired; clearing credentials")
        typingTracker.clear()
        sessionStore.clear()
    }

    private companion object {
        const val TAG = "SyncCoordinator"
    }
}
