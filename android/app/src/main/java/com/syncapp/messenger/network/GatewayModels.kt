package com.syncapp.messenger.network

import com.syncapp.messenger.network.protocol.ChatInfo
import com.syncapp.messenger.network.protocol.NewMessage
import com.syncapp.messenger.network.protocol.Presence
import com.syncapp.messenger.network.protocol.Profile
import com.syncapp.messenger.network.protocol.ProtocolException
import com.syncapp.messenger.network.protocol.ReadUpdate
import com.syncapp.messenger.network.protocol.SecretMsg
import com.syncapp.messenger.network.protocol.SendAck
import com.syncapp.messenger.network.protocol.Subscription
import com.syncapp.messenger.network.protocol.Typing

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
        /**
         * The second factor, sent on the RETRY after the server answered
         * TWO_FACTOR_REQUIRED.
         *
         * A client cannot know in advance whether an account enforces one — asking would
         * make the protocol an oracle for which accounts are protected — so the flow is
         * credentials, then "a code is needed", then both. A RECOVERY code is accepted
         * here too: somebody who has lost their phone is looking at the same prompt, and
         * making them find a different screen is how a recovery path goes unused.
         */
        val totpCode: String = "",
    ) : Credentials
}

/**
 * What AUTH_OK gave us. [token] is the bearer credential for later logins;
 * [resumeToken] replays a dropped session's missed frames instead of refetching
 * history, and the gateway mints a fresh one per login.
 *
 * The profile fields come from the same frame, which is what makes a token login
 * self-sufficient: on every launch after the first there is no password to derive
 * a username from, and no other message would tell us who we are.
 */
data class GatewaySession(
    val userId: String,
    val deviceId: String,
    val sessionId: String,
    val token: String,
    val resumeToken: String,
    val username: String = "",
    val displayName: String = "",
    val avatarRef: String = "",
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

    /**
     * One of our messages reached a recipient's device. Shares [ReadUpdate]'s shape
     * with a read receipt because it is the same kind of fact — a monotonic
     * per-chat cursor for one person — one step earlier.
     */
    data class DeliveryReceipt(val body: ReadUpdate) : ServerEvent

    data class TypingSignal(val body: Typing) : ServerEvent

    /**
     * Ciphertext relayed from a peer device in a secret chat.
     *
     * Opaque to everything above the crypto layer, including this type: the
     * server could not read it and neither can anything here until the ratchet
     * has run. Delivered per DEVICE, because each of a peer's devices runs its
     * own ratchet and a ciphertext sealed for one cannot be opened by another.
     */
    data class SecretMessage(val body: SecretMsg) : ServerEvent

    /**
     * Someone's online state changed. Delivered to the peers of a user's direct
     * chats — the audience "last seen" is actually shown to, and the only one
     * bounded enough to fan out on every connect and disconnect.
     */
    data class PresenceUpdate(val body: Presence) : ServerEvent

    data class ChatCreated(val body: ChatInfo) : ServerEvent

    /**
     * A profile changed. The gateway mirrors a PROFILE_SET to the author's other
     * devices, so a name changed on the phone does not stay stale on the desktop
     * until it happens to reconnect.
     */
    data class ProfileUpdated(val body: Profile) : ServerEvent

    /**
     * The tier and the entitlements it grants.
     *
     * A reply AND a push. A subscription changes without this client asking — a payment
     * settles minutes after the user closed the checkout page, a period lapses overnight
     * — and until the client hears about it it goes on offering features the server has
     * already started refusing, which reads as the app breaking rather than as a plan
     * ending.
     */
    data class SubscriptionChanged(val body: Subscription) : ServerEvent

    /** An error frame that correlated to no in-flight request. */
    data class Failure(val error: ProtocolException) : ServerEvent

    /** The session died underneath us (revoked, expired) — the app must re-login. */
    data class SessionExpired(val error: ProtocolException) : ServerEvent
}
