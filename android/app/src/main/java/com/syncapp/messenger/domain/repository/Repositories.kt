package com.syncapp.messenger.domain.repository

import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.Chat
import com.syncapp.messenger.domain.model.ChatFlags
import com.syncapp.messenger.domain.model.ChatKind
import com.syncapp.messenger.domain.model.ChatTarget
import com.syncapp.messenger.domain.model.DeviceSession
import com.syncapp.messenger.domain.model.Entitlements
import com.syncapp.messenger.domain.model.Message
import com.syncapp.messenger.domain.model.MessageAttachment
import com.syncapp.messenger.domain.model.PaymentIntent
import com.syncapp.messenger.domain.model.PaymentMethod
import com.syncapp.messenger.domain.model.PlanOffer
import com.syncapp.messenger.domain.model.PrivacySettings
import com.syncapp.messenger.domain.model.Session
import com.syncapp.messenger.domain.model.TwoFactorSetup
import com.syncapp.messenger.domain.model.TwoFactorState
import com.syncapp.messenger.domain.model.UserPresence
import com.syncapp.messenger.domain.model.UserSummary
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow

/**
 * Repository contracts.
 *
 * Every one of these hides the protocol completely: no envelope, no message type,
 * no `"@username"` addressing convention crosses this boundary. A ViewModel that
 * calls [MessageRepository.sendText] cannot tell whether the transport is a binary
 * WebSocket frame or a REST call — which is the point, because the transport here
 * is unusual enough that leaking it would spread through every screen.
 */

/** Coarse connection state for the UI: enough to show "connecting", not a state machine. */
enum class ConnectionStatus { OFFLINE, CONNECTING, ONLINE }

interface AuthRepository {
    /** The signed-in identity, or null when logged out. Drives the start destination. */
    val session: StateFlow<Session?>

    /** True until the stored session has been read once, so no screen flashes wrongly. */
    val restored: StateFlow<Boolean>

    val connection: StateFlow<ConnectionStatus>

    /**
     * Signs in. [totpCode] is empty on the first attempt.
     *
     * Two attempts by design, because a client must not be able to ask whether an
     * account has a second factor: that question is an oracle for which accounts are
     * protected. So the first attempt sends credentials, the server answers
     * `TWO_FACTOR_REQUIRED` when it wants more, and the second sends both.
     *
     * A RECOVERY code goes in the same field. Somebody who has lost their authenticator
     * is looking at the same prompt, and sending them hunting for a different screen is
     * how a recovery path ends up unused.
     */
    suspend fun login(username: String, password: String, totpCode: String = ""): Outcome<Session>

    suspend fun register(username: String, password: String): Outcome<Session>

    /**
     * Ends this session on the server, then locally: closes the socket, drops
     * tokens, wipes the cache and the secret-chat material.
     *
     * The server half is not optional. Without it "log out" only discarded the
     * token on this device while the session stayed valid until it expired, so a
     * phone that was lost rather than logged out kept access for the whole TTL.
     */
    suspend fun logout()

    /** Every live session of this account, so a person can recognise a device. */
    suspend fun listSessions(): Outcome<List<DeviceSession>>

    /**
     * Ends one session. Returns how many were actually ended — a client that
     * asked to sign out five devices and signed out one should say so.
     */
    suspend fun revokeSession(sessionId: String): Outcome<Int>

    /**
     * Ends every session except this one. What a person reaches for after
     * losing a device, and it deliberately leaves them signed in here.
     */
    suspend fun revokeOtherSessions(): Outcome<Int>

    /**
     * Erases the account and everything in it.
     *
     * [password] is re-checked by the server even though this connection is
     * already authenticated: a session token lives on the device, and someone
     * holding an unlocked phone must not be able to destroy the account behind
     * it with two taps. [reason] goes to the audit log and nowhere that
     * survives.
     */
    suspend fun deleteAccount(password: String, reason: String = ""): Outcome<Unit>

    /** This account's privacy settings, read from the server. */
    suspend fun privacy(): Outcome<PrivacySettings>

    /**
     * Replaces all three settings.
     *
     * All three every time, matching the protocol: a partial update would make
     * "nobody" indistinguishable from "not set".
     */
    suspend fun setPrivacy(settings: PrivacySettings): Outcome<PrivacySettings>
}

interface ChatRepository {
    fun observeChats(): Flow<List<Chat>>

    fun observeChat(chatId: String): Flow<Chat?>

    /** Refreshes the newest page of every chat this device knows about. */
    suspend fun refreshAll(): Outcome<Unit>

    suspend fun refresh(chatId: String): Outcome<Unit>

    suspend fun createGroup(title: String, memberRefs: List<String>, kind: ChatKind): Outcome<Chat>

    /** Joins by invite code or `@handle`; returns the chat id the server let us into. */
    suspend fun join(codeOrHandle: String): Outcome<String>

    /**
     * Resolves a peer to something a chat screen can open. Existing conversations
     * resolve to their id; a stranger resolves to a peer reference that becomes an
     * id on the first send.
     */
    suspend fun openDirectChat(username: String): Outcome<ChatTarget>

    /**
     * Emits the direct chat's real id once the server has assigned one.
     *
     * A conversation opened on a stranger has no id until its first message is
     * acknowledged, so a screen showing it needs to be told when that changes rather
     * than polling for it.
     */
    fun observeResolvedDirectChatId(username: String): Flow<String?>

    /** The archived pile, which [observeChats] excludes. */
    fun observeArchivedChats(): Flow<List<Chat>>

    /**
     * Starts a secret chat with one other account.
     *
     * A chat TYPE chosen at creation, alongside direct, group and channel — which is the
     * whole change. Secret chats used to exist only as a relay with no chat row behind
     * them: no entry in the list, no title, no unread count, no mute setting, no history.
     * So this client put them in a bottom sheet beside the product, which is exactly what
     * the data model said they were.
     *
     * Premium-gated on the server, which answers with a code of its own rather than
     * FORBIDDEN precisely so a client can route to an upgrade prompt instead of a dead
     * end. Idempotent: the gateway returns the canonical row for the pair.
     */
    suspend fun createSecretChat(peer: String): Outcome<Chat>

    /**
     * Sets this account's own mute / pin / archive state for a chat.
     *
     * All three at once, and absolute. proto3 has no field presence for scalars, so
     * "leave pinned alone" cannot be expressed on the wire — a partial setter would have
     * to invent the missing values, and inventing them is how muting a chat silently
     * unpins it. Callers read the current flags off the [Chat] they are looking at.
     *
     * Returns what the server STORED, not what was asked: it normalises a mute deadline
     * already in the past to "not muted", and two devices racing the same toggle converge
     * on the stored state rather than on whichever request was sent last.
     */
    suspend fun setChatFlags(chatId: String, flags: ChatFlags): Outcome<ChatFlags>
}

/**
 * Password, second factor, and the Premium tier.
 *
 * Its own contract rather than more methods on [AuthRepository], because the lifetime is
 * different: [AuthRepository] answers "who is signed in", which every screen needs, while
 * these serve two settings screens. Keeping them apart means the login path does not grow
 * a dependency on billing.
 */
interface AccountSecurityRepository {
    /**
     * The tier and what it grants, now and on every change.
     *
     * A flow rather than a suspend read because it arrives as a PUSH as well: a
     * subscription lapses or renews without this client asking, and a screen that read it
     * once at launch goes on offering features the server has started refusing.
     */
    val entitlements: StateFlow<Entitlements>

    /**
     * Changes the password, returning how many OTHER sessions were signed out.
     *
     * Signing them out is not optional: a password change that leaves an attacker's
     * session alive has achieved nothing. This session survives, so the user is not
     * thrown back to the login screen halfway through securing the account.
     */
    suspend fun changePassword(current: String, new: String): Outcome<Int>

    suspend fun twoFactorState(): Outcome<TwoFactorState>

    /**
     * Begins enrolment. Nothing is enforced yet — two steps on purpose, because a user
     * who scans a QR code wrongly and never confirms has not locked themselves out of
     * their own account.
     */
    suspend fun beginTwoFactor(): Outcome<TwoFactorSetup>

    /**
     * Confirms enrolment with a code from the authenticator, proving the secret reached
     * somewhere the user can read it.
     *
     * The RECOVERY CODES come back here, once. The stored form is an argon2id hash, so
     * there is no second showing — a screen that logs them and moves on has quietly
     * removed the account's only recovery path.
     */
    suspend fun confirmTwoFactor(code: String): Outcome<TwoFactorState>

    /**
     * Turns it off. Needs the password AND a code, so a stolen session cannot remove the
     * factor that keeps its holder out of the next login.
     */
    suspend fun disableTwoFactor(password: String, code: String): Outcome<TwoFactorState>

    suspend fun refreshEntitlements(): Outcome<Entitlements>

    /** The plans on offer for a country, with a price per payment method. */
    suspend fun plans(country: String): Outcome<List<PlanOffer>>

    /** Starts a payment and returns where to send the user. */
    suspend fun checkout(
        plan: String,
        method: PaymentMethod,
        country: String,
        returnUrl: String = "",
    ): Outcome<PaymentIntent>

    /** Cancels at period end. The time already paid for is not taken away. */
    suspend fun cancelSubscription(): Outcome<Entitlements>
}

interface MessageRepository {
    fun observeMessages(chatId: String): Flow<List<Message>>

    /** How far the other members have read — the read tick on our own messages. */
    fun observeOthersReadSeq(chatId: String): Flow<Long>

    /** How far their devices have received — the tick between sent and read. */
    fun observeOthersDeliveredSeq(chatId: String): Flow<Long>

    fun observeTyping(chatId: String): Flow<Set<String>>

    /**
     * Queues a message and tries to send it now. Succeeds offline: the message is
     * persisted in the outbox and flushed on reconnect, with the same dedup key, so
     * the server can never store it twice.
     */
    suspend fun sendText(
        target: ChatTarget,
        text: String,
        replyTo: String? = null,
    ): Outcome<Unit>

    suspend fun sendAttachment(
        target: ChatTarget,
        bytes: ByteArray,
        filename: String,
        mime: String,
        caption: String = "",
    ): Outcome<Unit>

    /** Retries a message that previously failed. */
    suspend fun retry(messageId: String): Outcome<Unit>

    /** Fetches one older page. Returns false when the server says there is no more. */
    suspend fun loadOlder(chatId: String, pageSize: Int = 50): Outcome<Boolean>

    suspend fun markRead(chatId: String, upToSeq: Long)

    fun sendTyping(chatId: String, active: Boolean)

    /** Flushes the outbox. Called when the connection becomes usable. */
    suspend fun flushOutbox()
}

interface UserRepository {
    fun observeKnownUsers(): Flow<List<UserSummary>>

    fun observeUser(userId: String): Flow<UserSummary?>

    /**
     * Someone's online state, or null while nothing is known about it. Ephemeral and
     * never cached across launches — a stale "online" is worse than no answer.
     */
    fun observePresence(userId: String): Flow<UserPresence?>

    /**
     * Reads a public profile. [target] is a user id or `"@username"`, which makes
     * this the user lookup as well — there is no directory and no prefix search, so
     * an exact handle is the only thing a client can know about a stranger.
     */
    suspend fun fetchProfile(target: String): Outcome<UserSummary>

    /** Publishes our own name and avatar. Null fields are left untouched. */
    suspend fun updateMyProfile(
        displayName: String? = null,
        avatarRef: String? = null,
        clearAvatar: Boolean = false,
    ): Outcome<UserSummary>

    suspend fun refreshMyProfile(): Outcome<UserSummary>

    suspend fun syncContacts(): Outcome<Unit>
}

interface MediaRepository {
    /** A signed, expiring download URL for a media ref, cached until it expires. */
    suspend fun downloadUrl(mediaRef: String): Outcome<String>

    suspend fun upload(bytes: ByteArray, filename: String, mime: String): Outcome<MessageAttachment>
}
