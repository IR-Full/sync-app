package com.synapse.messenger.network

import com.synapse.messenger.network.protocol.ChatInfo
import com.synapse.messenger.network.protocol.NewMessage
import com.synapse.messenger.network.protocol.Presence
import com.synapse.messenger.network.protocol.ProtocolException
import com.synapse.messenger.network.protocol.ReadUpdate
import com.synapse.messenger.network.protocol.SendAck
import com.synapse.messenger.network.protocol.Typing

/** Where the single gateway connection currently is. */
enum class ConnectionState {
    IDLE,
    CONNECTING,
    AUTHENTICATING,
    READY,
    RECONNECTING,
    CLOSED,
}

/** The credentials a connection attempt presents. */
sealed interface Credentials {
    data class Token(val token: String) : Credentials

    data class Password(
        val username: String,
        val password: String,
        val register: Boolean,
    ) : Credentials
}

/**
 * What AUTH_OK gave us. [token] is the bearer credential for later logins;
 * [resumeToken] replays a dropped session's missed frames instead of refetching
 * history, and the gateway mints a fresh one per login.
 */
data class GatewaySession(
    val userId: String,
    val deviceId: String,
    val sessionId: String,
    val token: String,
    val resumeToken: String,
)

/**
 * Unsolicited server frames, plus connection-level signals. Everything that is
 * not a correlated reply arrives here.
 */
sealed interface ServerEvent {
    data class Authenticated(val session: GatewaySession) : ServerEvent

    /** A message delivered by fanout (never a history backfill — those correlate to a request). */
    data class Message(val body: NewMessage) : ServerEvent

    /** A send acknowledged out of band, e.g. from another device of ours. */
    data class Acked(val body: SendAck) : ServerEvent

    data class ReadReceipt(val body: ReadUpdate) : ServerEvent

    data class TypingSignal(val body: Typing) : ServerEvent

    /**
     * Presence update. Declared by the protocol but currently unreachable: the
     * server publishes `user.presence` to the bus and nothing subscribes, so no
     * PRESENCE frame is ever delivered. Handled anyway — the day fanout picks the
     * subject up, this client already renders it.
     */
    data class PresenceUpdate(val body: Presence) : ServerEvent

    data class ChatCreated(val body: ChatInfo) : ServerEvent

    /** An error frame that correlated to no in-flight request. */
    data class Failure(val error: ProtocolException) : ServerEvent

    /** The session died underneath us (revoked, expired) — the app must re-login. */
    data class SessionExpired(val error: ProtocolException) : ServerEvent
}
