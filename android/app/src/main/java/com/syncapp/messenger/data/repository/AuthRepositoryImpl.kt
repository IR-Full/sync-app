package com.syncapp.messenger.data.repository

import com.syncapp.messenger.core.AppScope
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.core.runOutcome
import com.syncapp.messenger.data.secret.SecretTranscriptStore
import com.syncapp.messenger.data.sync.PresenceTracker
import com.syncapp.messenger.data.sync.TypingTracker
import com.syncapp.messenger.database.SyncAppDatabase
import com.syncapp.messenger.database.clearUserData
import com.syncapp.messenger.datastore.SecretKeyStore
import com.syncapp.messenger.datastore.SessionStore
import com.syncapp.messenger.domain.model.DeviceSession
import com.syncapp.messenger.domain.model.PrivacySettings
import com.syncapp.messenger.domain.model.Session
import com.syncapp.messenger.domain.model.Visibility
import com.syncapp.messenger.domain.repository.AuthRepository
import com.syncapp.messenger.domain.repository.ConnectionStatus
import com.syncapp.messenger.network.ConnectionState
import com.syncapp.messenger.network.Credentials
import com.syncapp.messenger.network.SyncAppGateway
import com.syncapp.messenger.network.UnexpectedReplyException
import com.syncapp.messenger.network.request
import com.syncapp.messenger.network.protocol.AccountDelete
import com.syncapp.messenger.network.protocol.MsgType
import com.syncapp.messenger.network.protocol.Privacy
import com.syncapp.messenger.network.protocol.PushToken
import com.syncapp.messenger.network.protocol.SessionRevoke
import com.syncapp.messenger.network.protocol.SessionRevoked
import com.syncapp.messenger.network.protocol.Sessions
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

@Singleton
class AuthRepositoryImpl @Inject constructor(
    private val secretKeys: SecretKeyStore,
    private val secretTranscripts: SecretTranscriptStore,
    private val gateway: SyncAppGateway,
    private val sessionStore: SessionStore,
    private val database: SyncAppDatabase,
    private val typingTracker: TypingTracker,
    private val presenceTracker: PresenceTracker,
    @param:AppScope private val scope: CoroutineScope,
) : AuthRepository {

    override val session: StateFlow<Session?> = sessionStore.session
        .map { stored ->
            stored?.let {
                Session(
                    userId = it.userId,
                    username = it.username,
                    deviceId = it.deviceId,
                    displayName = it.displayName,
                    avatarRef = it.avatarRef.takeIf { ref -> ref.isNotEmpty() },
                )
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

    override suspend fun login(
        username: String,
        password: String,
        totpCode: String,
    ): Outcome<Session> = authenticate(username, password, register = false, totpCode = totpCode)

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
        totpCode: String = "",
    ): Outcome<Session> = runOutcome {
        val normalized = username.trim().removePrefix("@").lowercase()
        // A previous account's cache must not survive into a new login on the same
        // phone: the rows are keyed by nothing but who fetched them.
        val previous = sessionStore.current()
        if (previous != null && previous.username != normalized) {
            database.clearUserData()
        }
        val gatewaySession = gateway.connect(
            Credentials.Password(
                username = normalized,
                password = password,
                register = register,
                totpCode = totpCode,
            ),
        )
        sessionStore.save(gatewaySession, gatewaySession.username.ifEmpty { normalized })
        Session(
            userId = gatewaySession.userId,
            username = gatewaySession.username.ifEmpty { normalized },
            deviceId = gatewaySession.deviceId,
            displayName = gatewaySession.displayName,
            avatarRef = gatewaySession.avatarRef.takeIf { it.isNotEmpty() },
        )
    }

    override suspend fun logout() {
        // Stop notifications at the source before the socket goes away: an empty
        // push token clears the device row server-side, so a logged-out phone does
        // not keep receiving somebody else's messages.
        runCatching { gateway.send(MsgType.PUSH_TOKEN, PushToken(token = "")) }
        // End the session on the SERVER, not only here. Forgetting the token
        // locally leaves it valid until it expires, which is the difference
        // between logging out and merely looking as though you did.
        //
        // Best-effort by necessity: logging out while offline has to work, and
        // the alternative — refusing — would strand someone on a device they
        // wanted to leave. The token is dropped locally either way, and the
        // session expires on its own eventually.
        runCatching {
            gateway.request<SessionRevoked>(
                MsgType.SESSION_REVOKE,
                SessionRevoke(sessionId = "", allIncludingCurrent = true),
            )
        }
        gateway.disconnect()
        typingTracker.clear()
        presenceTracker.clear()
        sessionStore.clear()
        database.clearUserData()
        // Secret-chat material goes too, and it is the one thing here that cannot
        // be recovered by logging back in: the identity, the ratchet sessions and
        // the transcripts exist nowhere else. Leaving them would hand the next
        // person to use this phone a decryptable archive of the last one's
        // private conversations.
        secretKeys.clear()
        secretTranscripts.clear()
    }

    override suspend fun listSessions(): Outcome<List<DeviceSession>> = runOutcome {
        // SESSION_LIST is an empty message: the type is the whole request. There
        // is no target field on purpose — a message that could name another
        // account would turn this into a device-enumeration primitive.
        gateway.request<Sessions>(MsgType.SESSION_LIST, null).sessions.map { info ->
            DeviceSession(
                sessionId = info.sessionId,
                deviceId = info.deviceId,
                platform = info.platform,
                createdAtMs = info.createdAt,
                expiresAtMs = info.expiresAt,
                current = info.current,
            )
        }
    }

    override suspend fun revokeSession(sessionId: String): Outcome<Int> = runOutcome {
        gateway.request<SessionRevoked>(
            MsgType.SESSION_REVOKE,
            SessionRevoke(sessionId = sessionId),
        ).revoked
    }

    override suspend fun revokeOtherSessions(): Outcome<Int> = runOutcome {
        // An empty session id means "every session but this one". Distinct from
        // allIncludingCurrent, which would sign this device out too.
        gateway.request<SessionRevoked>(
            MsgType.SESSION_REVOKE,
            SessionRevoke(sessionId = ""),
        ).revoked
    }

    override suspend fun deleteAccount(password: String, reason: String): Outcome<Unit> =
        runOutcome {
            // The envelope rather than the body: ACCOUNT_DELETED's fields can
            // all be at their protobuf defaults, which encodes to nothing and
            // decodes to null. The reply's TYPE is the confirmation, and a
            // failure arrives as an ERROR frame, which already throws.
            val reply = gateway.requestEnvelope(
                MsgType.ACCOUNT_DELETE,
                AccountDelete(password = password, reason = reason),
            )
            if (reply.type != MsgType.ACCOUNT_DELETED) {
                throw UnexpectedReplyException(MsgType.ACCOUNT_DELETE, reply.type)
            }
            // ACCOUNT_DELETED is the last frame this connection will ever carry —
            // every session was revoked before it was sent. Tearing down locally
            // is what stops the app waking up as a ghost of an account that no
            // longer exists.
            logout()
        }

    override suspend fun privacy(): Outcome<PrivacySettings> = runOutcome {
        gateway.request<Privacy>(MsgType.PRIVACY_GET, null).toDomain()
    }

    override suspend fun setPrivacy(settings: PrivacySettings): Outcome<PrivacySettings> =
        runOutcome {
            gateway.request<Privacy>(
                MsgType.PRIVACY_SET,
                Privacy(
                    lastSeen = settings.lastSeen.wire,
                    avatar = settings.avatar.wire,
                    groups = settings.groups.wire,
                ),
            ).toDomain()
        }

    /**
     * Reads the server's answer rather than echoing what we asked for.
     *
     * The two can differ — a value this build does not know arrives as
     * [Visibility.UNKNOWN] — and showing the request instead of the result would
     * tell the user a setting took effect that did not.
     */
    private fun Privacy.toDomain() = PrivacySettings(
        lastSeen = Visibility.fromWire(lastSeen),
        avatar = Visibility.fromWire(avatar),
        groups = Visibility.fromWire(groups),
    )
}
