package com.syncapp.messenger.data.sync

import android.util.Log
import com.syncapp.messenger.core.AppScope
import com.syncapp.messenger.data.secret.SecretChatEngine
import com.syncapp.messenger.data.secret.SecretTranscriptStore
import com.syncapp.messenger.data.repository.MessageRepositoryImpl
import com.syncapp.messenger.database.SyncAppDatabase
import com.syncapp.messenger.datastore.SessionStore
import com.syncapp.messenger.network.ConnectionState
import com.syncapp.messenger.network.Credentials
import com.syncapp.messenger.network.ServerEvent
import com.syncapp.messenger.network.SyncAppGateway
import com.syncapp.messenger.network.protocol.Profile
import com.syncapp.messenger.push.PushTokenRegistrar
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
    private val gateway: SyncAppGateway,
    private val sessionStore: SessionStore,
    private val database: SyncAppDatabase,
    private val ingestor: MessageIngestor,
    private val profiles: ProfileFetcher,
    private val typingTracker: TypingTracker,
    private val presenceTracker: PresenceTracker,
    private val messageRepository: MessageRepositoryImpl,
    private val userSync: ContactSyncer,
    private val pushTokens: PushTokenRegistrar,
    private val secretChats: SecretChatEngine,
    private val secretTranscripts: SecretTranscriptStore,
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
                is ServerEvent.DeliveryReceipt -> ingestor.ingestDelivery(event.body)
                is ServerEvent.TypingSignal ->
                    typingTracker.onTyping(event.body.chatId, event.body.userId, event.body.active)
                is ServerEvent.ProfileUpdated -> profiles.store(event.body)
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
                // Decrypted the moment it lands. The ratchet advances with each
                // message, so deferring the work would reorder the chain — and the
                // server holds no copy to re-deliver, so a dropped one is gone.
                is ServerEvent.SecretMessage -> onSecretMessage(event)
                is ServerEvent.PresenceUpdate -> presenceTracker.onPresence(
                    userId = event.body.userId,
                    online = event.body.online,
                    lastSeenMs = event.body.lastSeenMs,
                )
                // Nothing to do here: the gateway broadcasts this on its own flow, and
                // AccountSecurityRepository holds the entitlement state. Listed
                // explicitly rather than swept up by an `else` so the next event type
                // added to the protocol is a compile error in this file — which is how
                // the ones above got handled at all.
                is ServerEvent.SubscriptionChanged -> Unit
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
        // AUTH_OK now carries the identity itself, so a token login knows who it is
        // without inferring anything from the credentials it did not use.
        sessionStore.save(event.session, event.session.username.takeIf { it.isNotEmpty() })
        profiles.store(
            Profile(
                userId = event.session.userId,
                username = event.session.username,
                displayName = event.session.displayName,
                avatarRef = event.session.avatarRef,
            ),
        )
        scope.launch { messageRepository.flushOutbox() }
        scope.launch { userSync.syncQuietly() }
        scope.launch { pushTokens.sync() }
        // Republished on every connect: KEY_PUBLISH is fire-and-forget, so
        // "published" cannot be confirmed, and the directory upserts. This is
        // also where rotation happens, so a device that has been offline past
        // its signed prekey's lifetime refreshes it on the way back.
        scope.launch { runCatching { secretChats.publishKeys() } }
        // The transcripts live only on this device, so they are read from disk
        // rather than fetched — there is nothing on the server to fetch.
        scope.launch { secretTranscripts.load(event.session.userId) }
        // Self-destructing messages carry a deadline, and the server reaps its own
        // copy — a cached one that outlived it would make this device the only place
        // the message still exists.
        scope.launch { database.messageDao().purgeExpired(System.currentTimeMillis()) }
    }

    /**
     * Opens one inbound secret message and files it under the peer.
     *
     * A failure is recorded rather than dropped. The usual cause is a peer that
     * reinstalled and started a fresh session against a prekey this device no
     * longer holds; showing nothing would leave the reader believing the other
     * person never wrote, which is worse than an explicit "could not decrypt".
     */
    private suspend fun onSecretMessage(event: ServerEvent.SecretMessage) {
        val opened = runCatching { secretChats.receive(event.body) }.getOrNull()
        secretTranscripts.append(
            peerId = event.body.fromUserId,
            text = opened?.plaintext.orEmpty(),
            outgoing = false,
            failed = opened == null,
        )
    }

    /**
     * The session died server-side (revoked, expired). Tokens go, cached rows stay:
     * the same user logging back in should not have to refetch their history, and a
     * *different* user logging in wipes the cache at login (see AuthRepositoryImpl).
     */
    private suspend fun onSessionExpired() {
        Log.i(TAG, "session expired; clearing credentials")
        typingTracker.clear()
        presenceTracker.clear()
        sessionStore.clear()
    }

    private companion object {
        const val TAG = "SyncCoordinator"
    }
}
