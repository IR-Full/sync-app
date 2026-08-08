package com.synapse.messenger.data.repository

import com.synapse.messenger.core.AppScope
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.core.runOutcome
import com.synapse.messenger.data.sync.TypingTracker
import com.synapse.messenger.database.SynapseDatabase
import com.synapse.messenger.database.clearUserData
import com.synapse.messenger.datastore.SessionStore
import com.synapse.messenger.domain.model.Session
import com.synapse.messenger.domain.repository.AuthRepository
import com.synapse.messenger.domain.repository.ConnectionStatus
import com.synapse.messenger.network.ConnectionState
import com.synapse.messenger.network.Credentials
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.protocol.MsgType
import com.synapse.messenger.network.protocol.PushToken
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

@Singleton
class AuthRepositoryImpl @Inject constructor(
    private val gateway: SynapseGateway,
    private val sessionStore: SessionStore,
    private val database: SynapseDatabase,
    private val typingTracker: TypingTracker,
    @param:AppScope private val scope: CoroutineScope,
) : AuthRepository {

    override val session: StateFlow<Session?> = sessionStore.session
        .map { stored ->
            stored?.let {
                Session(userId = it.userId, username = it.username, deviceId = it.deviceId)
            }
        }
        .stateIn(scope, SharingStarted.Eagerly, null)

    /**
     * Whether the stored session has been read at least once. Without it the app
     * would render the login screen for a frame on every cold start, because
     * "no session yet" and "no session at all" look identical.
     */
    override val restored: StateFlow<Boolean> = sessionStore.session
        .map { true }
        .stateIn(scope, SharingStarted.Eagerly, false)

    override val connection: StateFlow<ConnectionStatus> = gateway.state
        .map { state ->
            when (state) {
                ConnectionState.READY -> ConnectionStatus.ONLINE
                ConnectionState.CONNECTING,
                ConnectionState.AUTHENTICATING,
                ConnectionState.RECONNECTING,
                -> ConnectionStatus.CONNECTING
                ConnectionState.IDLE, ConnectionState.CLOSED -> ConnectionStatus.OFFLINE
            }
        }
        .stateIn(scope, SharingStarted.Eagerly, ConnectionStatus.OFFLINE)

    override suspend fun login(username: String, password: String): Outcome<Session> =
        authenticate(username, password, register = false)

    override suspend fun register(username: String, password: String): Outcome<Session> =
        authenticate(username, password, register = true)

    /**
     * Register and login are separate intents all the way down: the gateway fails a
     * login for a missing account instead of creating one, so this client must not
     * paper over the difference either (it would enable account-existence probing
     * and let a typo'd username silently become a new account).
     */
    private suspend fun authenticate(
        username: String,
        password: String,
        register: Boolean,
    ): Outcome<Session> = runOutcome {
        val normalized = username.trim().removePrefix("@").lowercase()
        // A previous account's cache must not survive into a new login on the same
        // phone: the rows are keyed by nothing but who fetched them.
        val previous = sessionStore.current()
        if (previous != null && previous.username != normalized) {
            database.clearUserData()
        }
        val gatewaySession = gateway.connect(
            Credentials.Password(username = normalized, password = password, register = register),
        )
        sessionStore.save(gatewaySession, normalized)
        Session(
            userId = gatewaySession.userId,
            username = normalized,
            deviceId = gatewaySession.deviceId,
        )
    }

    override suspend fun logout() {
        // Stop notifications at the source before the socket goes away: an empty
        // push token clears the device row server-side, so a logged-out phone does
        // not keep receiving somebody else's messages.
        runCatching { gateway.send(MsgType.PUSH_TOKEN, PushToken(token = "")) }
        gateway.disconnect()
        typingTracker.clear()
        sessionStore.clear()
        database.clearUserData()
    }
}
