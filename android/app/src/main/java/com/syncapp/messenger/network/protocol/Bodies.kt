package com.syncapp.messenger.network.protocol

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.protobuf.ProtoNumber

/**
 * Envelope bodies, hand-mapped from `server/proto/syncapp/v1/body.proto`.
 *
 * The gateway installs the protobuf codec unconditionally
 * (`pkg/wire/protocodec.go`'s package `init()`) — there is no JSON to negotiate.
 * These classes carry the schema's field numbers via [ProtoNumber], which is the
 * only thing the wire format cares about; the Kotlin names are ours.
 *
 * Two rules keep this compatible with the Go side:
 *
 *  - **Every property has a default.** proto3 omits fields equal to their zero
 *    value, so a decoder that required them would break on the first empty
 *    string the server sends. Symmetrically, `ProtoBuf` is configured with
 *    `encodeDefaults = false` (its default) so our own frames stay proto3-shaped.
 *  - **`uint64` maps to `Long`.** Both are varints on the wire and the values in
 *    play (per-chat sequences, unix millis) are nowhere near 2^63. Ids travel as
 *    decimal strings, so nothing that could overflow arrives as an integer.
 */

// --- Handshake ---

@Serializable
data class Hello(
    @ProtoNumber(1) @SerialName("client_version") val clientVersion: String = "",
    @ProtoNumber(2) @SerialName("device_id") val deviceId: String = "",
    @ProtoNumber(3) val platform: String = "",
    @ProtoNumber(4) val caps: Int = 0,
    @ProtoNumber(5) @SerialName("resume_token") val resumeToken: String = "",
)

@Serializable
data class Welcome(
    @ProtoNumber(1) @SerialName("server_version") val serverVersion: String = "",
    @ProtoNumber(2) @SerialName("session_id") val sessionId: String = "",
    @ProtoNumber(3) val caps: Int = 0,
    @ProtoNumber(4) @SerialName("heartbeat_ms") val heartbeatMs: Int = 0,
    @ProtoNumber(5) @SerialName("max_inflight") val maxInflight: Int = 0,
    @ProtoNumber(6) @SerialName("resume_supported") val resumeSupported: Boolean = false,
)

/**
 * Bearer token, or credentials. [register] is an explicit intent: the gateway
 * never silently creates an account on a failed login, so the two flows are
 * separate calls rather than one "upsert".
 */
@Serializable
data class Auth(
    @ProtoNumber(1) val token: String = "",
    @ProtoNumber(2) val username: String = "",
    @ProtoNumber(3) val password: String = "",
    @ProtoNumber(4) val register: Boolean = false,
    /**
     * Honoured on registration only — afterwards PROFILE_SET is the single writer
     * of the field, so a stale client cannot silently revert a name changed
     * elsewhere. The server falls back to the username when this is empty.
     */
    @ProtoNumber(5) @SerialName("display_name") val displayName: String = "",
    /**
     * The second factor, sent on the RETRY after the server answered
     * `ErrTwoFactorRequired`.
     *
     * A client cannot know in advance whether an account enforces one — asking would
     * make the protocol an oracle for which accounts are protected — so the flow is
     * credentials, then "a code is needed", then both. A RECOVERY code is accepted
     * here too: the user who lost their phone is looking at the same prompt, and
     * making them find a different screen is how a recovery path goes unused.
     */
    @ProtoNumber(6) @SerialName("totp_code") val totpCode: String = "",
)

/**
 * Identity, and now who that identity *is*. The last three fields matter for
 * every launch after the first: a client that authenticated by token would
 * otherwise know its user id and nothing else about itself.
 */
@Serializable
data class AuthOk(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) @SerialName("device_id") val deviceId: String = "",
    @ProtoNumber(3) @SerialName("session_id") val sessionId: String = "",
    @ProtoNumber(4) val token: String = "",
    @ProtoNumber(5) @SerialName("resume_token") val resumeToken: String = "",
    @ProtoNumber(6) val username: String = "",
    @ProtoNumber(7) @SerialName("display_name") val displayName: String = "",
    @ProtoNumber(8) @SerialName("avatar_ref") val avatarRef: String = "",
)

@Serializable
data class Resume(
    @ProtoNumber(1) @SerialName("resume_token") val resumeToken: String = "",
    @ProtoNumber(2) @SerialName("last_ack_seq") val lastAckSeq: Long = 0,
)

@Serializable
data class ResumeOk(
    @ProtoNumber(1) @SerialName("session_id") val sessionId: String = "",
    @ProtoNumber(2) @SerialName("from_seq") val fromSeq: Long = 0,
    /**
     * The token to use NEXT time, because resuming CONSUMES the one just sent.
     *
     * Storing the old one after a successful resume does not merely fail the next
     * resume — it looks to the server exactly like a thief replaying a consumed
     * token, which ends the session. Rotation is the point: an unrotated resume token
     * granted access for the session's whole 14-day life from a single capture, and
     * its use was invisible because the real client kept working alongside it.
     */
    @ProtoNumber(3) @SerialName("resume_token") val resumeToken: String = "",
)

@Serializable
data class ProtocolError(
    @ProtoNumber(1) val code: Int = 0,
    @ProtoNumber(2) val message: String = "",
    @ProtoNumber(3) @SerialName("retry_after_ms") val retryAfterMs: Int = 0,
)

// --- Messaging ---

/**
 * Typed media metadata. The bytes go through the media pipeline first
 * (MEDIA_INIT → PUT → media_ref); this only describes them, which is what lets a
 * client draw a voice waveform or a file card before downloading anything.
 */
@Serializable
data class Attachment(
    @ProtoNumber(1) val kind: String = "",
    @ProtoNumber(2) @SerialName("media_ref") val mediaRef: String = "",
    @ProtoNumber(3) val filename: String = "",
    @ProtoNumber(4) val mime: String = "",
    @ProtoNumber(5) val size: Long = 0,
    @ProtoNumber(6) @SerialName("duration_ms") val durationMs: Long = 0,
    @ProtoNumber(7) val waveform: List<Int> = emptyList(),
    @ProtoNumber(8) val width: Int = 0,
    @ProtoNumber(9) val height: Int = 0,
    @ProtoNumber(10) @SerialName("thumb_ref") val thumbRef: String = "",
)

@Serializable
data class ForwardOrigin(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(3) @SerialName("sender_id") val senderId: String = "",
)

/**
 * A send request. [chatId] accepts either a numeric chat id or `"@username"` —
 * the gateway resolves the latter to the one direct chat for the pair, creating
 * it if this is the first message. [dedupKey] is our idempotency key: the server
 * maps (device, dedupKey) → message id, so retrying a send after a dropped
 * socket can never duplicate it.
 */
@Serializable
data class Send(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("dedup_key") val dedupKey: String = "",
    @ProtoNumber(3) val text: String = "",
    @ProtoNumber(4) @SerialName("media_ref") val mediaRef: String = "",
    @ProtoNumber(5) @SerialName("reply_to") val replyTo: String = "",
    @ProtoNumber(6) val attachment: Attachment? = null,
    @ProtoNumber(7) @SerialName("ttl_seconds") val ttlSeconds: Int = 0,
)

@Serializable
data class SendAck(
    @ProtoNumber(1) @SerialName("dedup_key") val dedupKey: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(3) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(4) @SerialName("chat_seq") val chatSeq: Long = 0,
    @ProtoNumber(5) val timestamp: Long = 0,
    @ProtoNumber(6) val duplicate: Boolean = false,
)

@Serializable
data class NewMessage(
    @ProtoNumber(1) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) @SerialName("sender_id") val senderId: String = "",
    @ProtoNumber(4) @SerialName("chat_seq") val chatSeq: Long = 0,
    @ProtoNumber(5) val text: String = "",
    @ProtoNumber(6) @SerialName("media_ref") val mediaRef: String = "",
    @ProtoNumber(7) @SerialName("reply_to") val replyTo: String = "",
    @ProtoNumber(8) val edited: Boolean = false,
    @ProtoNumber(9) val deleted: Boolean = false,
    @ProtoNumber(10) val timestamp: Long = 0,
    @ProtoNumber(11) val attachment: Attachment? = null,
    @ProtoNumber(12) @SerialName("thread_root") val threadRoot: String = "",
    @ProtoNumber(13) @SerialName("reply_count") val replyCount: Int = 0,
    @ProtoNumber(14) val forward: ForwardOrigin? = null,
    @ProtoNumber(15) @SerialName("expires_at") val expiresAt: Long = 0,
)

@Serializable
data class Read(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("up_to_message_id") val upToMessageId: String = "",
    @ProtoNumber(3) @SerialName("up_to_chat_seq") val upToChatSeq: Long = 0,
)

@Serializable
data class ReadUpdate(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(3) @SerialName("up_to_chat_seq") val upToChatSeq: Long = 0,
)

@Serializable
data class Typing(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(3) val active: Boolean = false,
)

@Serializable
data class Presence(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) val online: Boolean = false,
    @ProtoNumber(3) @SerialName("last_seen_ms") val lastSeenMs: Long = 0,
)

@Serializable
data class Edit(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(3) val text: String = "",
)

@Serializable
data class Delete(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(3) @SerialName("for_all") val forAll: Boolean = false,
)

/** Backfill request. [beforeSeq] 0 means "the newest page". */
@Serializable
data class History(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("before_seq") val beforeSeq: Long = 0,
    @ProtoNumber(3) val limit: Int = 0,
)

/**
 * Terminates a streamed history page. Note [chatId] echoes what the *request*
 * asked for — if the request addressed `"@username"`, so does this reply; only
 * the NEW frames carry the resolved numeric id.
 */
@Serializable
data class HistoryOk(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("next_before") val nextBefore: Long = 0,
    @ProtoNumber(3) val done: Boolean = false,
)

// --- Media ---

@Serializable
data class MediaInit(
    @ProtoNumber(1) val filename: String = "",
    @ProtoNumber(2) @SerialName("content_type") val contentType: String = "",
    @ProtoNumber(3) val size: Long = 0,
)

@Serializable
data class MediaTicket(
    @ProtoNumber(1) @SerialName("media_ref") val mediaRef: String = "",
    @ProtoNumber(2) @SerialName("upload_url") val uploadUrl: String = "",
    @ProtoNumber(3) @SerialName("expires_at") val expiresAt: Long = 0,
)

@Serializable
data class MediaFetch(
    @ProtoNumber(1) @SerialName("media_ref") val mediaRef: String = "",
)

@Serializable
data class MediaUrl(
    @ProtoNumber(1) @SerialName("media_ref") val mediaRef: String = "",
    @ProtoNumber(2) @SerialName("download_url") val downloadUrl: String = "",
    @ProtoNumber(3) @SerialName("expires_at") val expiresAt: Long = 0,
)

// --- Search ---

@Serializable
data class Search(
    @ProtoNumber(1) val query: String = "",
    @ProtoNumber(2) val limit: Int = 0,
    /**
     * Optional narrowing. Membership still bounds the result: [chatId] is CHECKED
     * against the caller's membership rather than trusted as a filter, because a chat
     * id is guessable and a filter is not an authorization.
     */
    @ProtoNumber(3) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(4) @SerialName("sender_id") val senderId: String = "",
)

@Serializable
data class SearchHit(
    @ProtoNumber(1) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) @SerialName("sender_id") val senderId: String = "",
    @ProtoNumber(4) val seq: Long = 0,
    @ProtoNumber(5) val text: String = "",
    /**
     * The ranking key, and the one a client can re-sort by.
     *
     * [seq] cannot order results that span chats — it counts WITHIN a chat, so a chat
     * with a million messages outranks every other by construction. That was the
     * server's own bug before it started ranking by relevance and time.
     */
    @ProtoNumber(6) @SerialName("created_at") val createdAt: Long = 0,
)

@Serializable
data class SearchResults(
    @ProtoNumber(1) val query: String = "",
    @ProtoNumber(2) val hits: List<SearchHit> = emptyList(),
)

// --- Contacts ---

/** [target] is a user id or `"@username"`; the reply resolves it to a user id. */
@Serializable
data class ContactAdd(
    @ProtoNumber(1) val target: String = "",
    @ProtoNumber(2) val name: String = "",
)

@Serializable
data class ContactRemove(
    @ProtoNumber(1) val target: String = "",
)

@Serializable
data class ContactSync(
    @ProtoNumber(1) val since: Long = 0,
)

@Serializable
data class Contact(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) val name: String = "",
    @ProtoNumber(3) val blocked: Boolean = false,
    @ProtoNumber(4) @SerialName("updated_at") val updatedAt: Long = 0,
)

@Serializable
data class ContactList(
    @ProtoNumber(1) val contacts: List<Contact> = emptyList(),
    @ProtoNumber(2) val cursor: Long = 0,
)

@Serializable
data class Block(
    @ProtoNumber(1) val target: String = "",
    @ProtoNumber(2) val blocked: Boolean = false,
)

// --- Chats, joins, push ---

/** [members] are user ids or `"@username"` — the gateway resolves them. */
@Serializable
data class ChatCreate(
    @ProtoNumber(1) val type: String = "",
    @ProtoNumber(2) val title: String = "",
    @ProtoNumber(3) val members: List<String> = emptyList(),
)

@Serializable
data class ChatInfo(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val type: String = "",
    @ProtoNumber(3) val title: String = "",
    @ProtoNumber(4) @SerialName("owner_id") val ownerId: String = "",
)

/** Join by invite [code], or by `"@handle"` for a chat with a public handle. */
@Serializable
data class Join(
    @ProtoNumber(1) val code: String = "",
    @ProtoNumber(2) val handle: String = "",
)

@Serializable
data class InviteLink(
    @ProtoNumber(1) val code: String = "",
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) @SerialName("expires_at") val expiresAt: Long = 0,
    @ProtoNumber(4) @SerialName("max_uses") val maxUses: Int = 0,
    @ProtoNumber(5) val uses: Int = 0,
)

@Serializable
data class Invites(
    @ProtoNumber(1) val links: List<InviteLink> = emptyList(),
    @ProtoNumber(2) @SerialName("joined_chat") val joinedChat: String = "",
)

/** An empty token clears push for this device — turning notifications off at the source. */
@Serializable
data class PushToken(
    @ProtoNumber(1) val token: String = "",
)

// --- Chat list ---

/**
 * Keyset pagination over a list ordered by ACTIVITY.
 *
 * Both halves of the cursor are needed. The list reorders as messages arrive, so a
 * cursor naming only a chat id skips and repeats rows exactly when the account is
 * busy — which is when a user is most likely to be scrolling it.
 */
@Serializable
data class ChatList(
    @ProtoNumber(1) val after: String = "",
    @ProtoNumber(2) val limit: Int = 0,
    @ProtoNumber(3) @SerialName("after_activity") val afterActivity: Long = 0,
    /** Lists the archived pile instead of hiding it. */
    @ProtoNumber(4) @SerialName("include_archived") val includeArchived: Boolean = false,
)

/**
 * One row of the chat list — enough to render an entry without a round trip per
 * chat. [peerId] is filled for direct chats only, which is what finally gives a
 * 1:1 conversation something to be named after.
 */
@Serializable
data class ChatSummary(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val type: String = "",
    @ProtoNumber(3) val title: String = "",
    @ProtoNumber(4) @SerialName("owner_id") val ownerId: String = "",
    @ProtoNumber(5) val username: String = "",
    /** The chat's newest position, so a client knows how far it has to backfill. */
    @ProtoNumber(6) @SerialName("last_seq") val lastSeq: Long = 0,
    @ProtoNumber(7) @SerialName("my_role") val myRole: String = "",
    @ProtoNumber(8) @SerialName("peer_id") val peerId: String = "",

    /*
     * What a chat list actually draws, and none of it used to be sent.
     *
     * A client that wanted a preview, an unread badge or a sort order had to call
     * HISTORY per chat to work it out — so the server's N+1 moved onto the network and
     * became one round trip per row.
     */
    @ProtoNumber(9) @SerialName("last_message") val lastMessage: NewMessage? = null,
    @ProtoNumber(10) @SerialName("unread_count") val unreadCount: Long = 0,
    /**
     * The sort key AND the paging cursor. Falls back to the chat's creation time, so a
     * brand-new empty chat appears at the top rather than the bottom.
     */
    @ProtoNumber(11) @SerialName("last_activity_at") val lastActivityAt: Long = 0,
    /**
     * This account's own settings. A DEADLINE for the mute rather than a flag: "for
     * eight hours" is what muting usually means and a boolean cannot say it.
     */
    @ProtoNumber(12) @SerialName("muted_until") val mutedUntil: Long = 0,
    @ProtoNumber(13) val pinned: Boolean = false,
    @ProtoNumber(14) val archived: Boolean = false,
)

@Serializable
data class Chats(
    @ProtoNumber(1) val chats: List<ChatSummary> = emptyList(),
    @ProtoNumber(2) @SerialName("next_after") val nextAfter: String = "",
    @ProtoNumber(3) val done: Boolean = false,
    /** Completes the cursor; echoed back in the next [ChatList]. */
    @ProtoNumber(4) @SerialName("next_after_activity") val nextAfterActivity: Long = 0,
)

// --- Profiles ---

/** [target] is a user id or `"@username"`; empty means "me". */
@Serializable
data class ProfileGet(
    @ProtoNumber(1) val target: String = "",
)

/**
 * Updates the caller's own profile — there is no way to address anyone else's.
 * Empty fields mean "leave as is" (proto3 cannot tell absent from empty), which
 * is why clearing the avatar is a flag rather than an empty ref.
 */
@Serializable
data class ProfileSet(
    @ProtoNumber(1) @SerialName("display_name") val displayName: String = "",
    @ProtoNumber(2) @SerialName("avatar_ref") val avatarRef: String = "",
    @ProtoNumber(3) @SerialName("clear_avatar") val clearAvatar: Boolean = false,
)

/** [avatarRef] points into the media service; fetch a URL for it with MEDIA_FETCH. */
@Serializable
data class Profile(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) val username: String = "",
    @ProtoNumber(3) @SerialName("display_name") val displayName: String = "",
    @ProtoNumber(4) @SerialName("avatar_ref") val avatarRef: String = "",
    /** A paying account, for a badge beside the name. False where nothing is sold. */
    @ProtoNumber(5) val premium: Boolean = false,
)

// --- Secret chats ---
//
// Keys travel as standard base64 with padding (Go's base64.StdEncoding), which
// the gateway validates on publish: a url-safe alphabet or a wrong length is
// rejected rather than stored, so a mistake here shows up immediately instead of
// as an unopenable message on somebody else's phone.

/** Uploads this device's long-term keys and a batch of one-time prekeys. */
@Serializable
data class KeyPublish(
    @ProtoNumber(1) @SerialName("identity_key") val identityKey: String = "",
    @ProtoNumber(2) @SerialName("signing_key") val signingKey: String = "",
    @ProtoNumber(3) @SerialName("signed_prekey") val signedPrekey: String = "",
    @ProtoNumber(4) @SerialName("signed_prekey_sig") val signedPrekeySig: String = "",
    @ProtoNumber(5) val prekeys: List<String> = emptyList(),
)

/**
 * Requests a peer's prekeys.
 *
 * Reused by KEY_FETCH_ALL with an empty `deviceId`, following the protocol's own
 * convention of reusing a body under a distinct type.
 */
@Serializable
data class KeyFetch(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) @SerialName("device_id") val deviceId: String = "",
)

/** One consumable X3DH prekey bundle for a peer device. */
@Serializable
data class KeyBundle(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) @SerialName("device_id") val deviceId: String = "",
    @ProtoNumber(3) @SerialName("identity_key") val identityKey: String = "",
    @ProtoNumber(4) @SerialName("signing_key") val signingKey: String = "",
    @ProtoNumber(5) @SerialName("signed_prekey") val signedPrekey: String = "",
    @ProtoNumber(6) @SerialName("signed_prekey_sig") val signedPrekeySig: String = "",
    @ProtoNumber(7) @SerialName("one_time_prekey") val oneTimePrekey: String = "",
)

/** Every device's bundle for one user — the multi-device fanout a sender needs. */
@Serializable
data class KeyBundles(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) val bundles: List<KeyBundle> = emptyList(),
)

/**
 * What the directory holds for this device, answering KEY_PUBLISH.
 *
 * The reply exists because of a gap invisible from both ends. One-time prekeys are
 * consumed one per peer that starts a session, so a popular device runs its own batch
 * down; with an empty batch X3DH silently drops from four Diffie-Hellmans to three,
 * which is weaker and reported to nobody — the peer cannot tell and the owner is
 * never told. Only the OWNER can refill, and the owner is not the party fetching.
 */
@Serializable
data class KeyState(
    /**
     * Prekeys held AFTER this publish, capped at the directory's per-device ceiling. A
     * client tops up below its own threshold rather than at zero: zero has already cost
     * somebody the stronger handshake.
     */
    @ProtoNumber(1) @SerialName("one_time_prekeys_left") val oneTimePrekeysLeft: Int = 0,
    /**
     * How long ago the STORED signed prekey first appeared; 0 when this publish
     * introduced it. A device cannot work this out for itself, because it cannot know
     * whether its own last rotation ever landed.
     */
    @ProtoNumber(2) @SerialName("signed_prekey_age_ms") val signedPrekeyAgeMs: Long = 0,
    /**
     * How many prekeys from this frame the directory kept. Lower than what was sent when
     * a cap trimmed it — which a client holding the private halves needs to know, or it
     * keeps private keys for public ones nobody can fetch and believes its reserve is
     * larger than it is.
     */
    @ProtoNumber(3) val accepted: Int = 0,
)

/**
 * Opaque ciphertext relayed between two devices.
 *
 * `ratchetHeader` carries the X3DH bootstrap on the first message of a session
 * and the bare ratchet header afterwards; both are base64 inside a small JSON
 * envelope, which the server never reads.
 */
@Serializable
data class SecretMsg(
    @ProtoNumber(1) @SerialName("to_user_id") val toUserId: String = "",
    @ProtoNumber(2) @SerialName("to_device_id") val toDeviceId: String = "",
    @ProtoNumber(3) @SerialName("from_user_id") val fromUserId: String = "",
    @ProtoNumber(4) @SerialName("from_device_id") val fromDeviceId: String = "",
    /*
     * The LEGACY text form, and its two halves are encoded DIFFERENTLY. This is worth
     * being explicit about because the schema comment used to claim both were base64,
     * which is wrong and rejects every real message:
     *
     *   - ratchetHeader is TEXT. Every client puts a JSON object there (the X3DH
     *     bootstrap on the first message, the ratchet header afterwards) and the
     *     receiver parses it as JSON.
     *   - ciphertext IS base64, because it is genuinely opaque bytes.
     */
    @ProtoNumber(5) @SerialName("ratchet_header") val ratchetHeader: String = "",
    @ProtoNumber(6) val ciphertext: String = "",
    /**
     * Set ONLY on a replay out of the offline queue, and echoed back in [SecretAcked]
     * so the server can drop the row. Empty means this arrived live and there is
     * nothing to acknowledge.
     */
    @ProtoNumber(7) @SerialName("queue_id") val queueId: String = "",
    /*
     * The BINARY form, sent to and by peers that negotiated `CAP_SECRET_QUEUE`.
     *
     * base64 cost a third of every secret message plus an encode and a decode at each
     * end — in a protocol that is binary everywhere else specifically to avoid that.
     * Only ONE form is ever populated: sending both would double the payload, which is
     * the opposite of the point.
     */
    @ProtoNumber(8) @SerialName("ratchet_header_bin") val ratchetHeaderBin: ByteArray? = null,
    @ProtoNumber(9) @SerialName("ciphertext_bin") val ciphertextBin: ByteArray? = null,
) {
    /**
     * ByteArray has reference equality, so a data class holding one needs these.
     *
     * Written out rather than left to the compiler because the generated versions
     * compare the arrays by identity, which makes two decodes of the same frame
     * unequal — and that difference shows up as a de-duplication check that never
     * matches.
     */
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other !is SecretMsg) return false
        return toUserId == other.toUserId &&
            toDeviceId == other.toDeviceId &&
            fromUserId == other.fromUserId &&
            fromDeviceId == other.fromDeviceId &&
            ratchetHeader == other.ratchetHeader &&
            ciphertext == other.ciphertext &&
            queueId == other.queueId &&
            ratchetHeaderBin.contentEquals(other.ratchetHeaderBin) &&
            ciphertextBin.contentEquals(other.ciphertextBin)
    }

    override fun hashCode(): Int {
        var result = toUserId.hashCode()
        result = 31 * result + toDeviceId.hashCode()
        result = 31 * result + fromUserId.hashCode()
        result = 31 * result + fromDeviceId.hashCode()
        result = 31 * result + ratchetHeader.hashCode()
        result = 31 * result + ciphertext.hashCode()
        result = 31 * result + queueId.hashCode()
        result = 31 * result + (ratchetHeaderBin?.contentHashCode() ?: 0)
        result = 31 * result + (ciphertextBin?.contentHashCode() ?: 0)
        return result
    }
}

/**
 * What the relay did with a [SecretMsg].
 *
 * It used to answer NOTHING, so a client could not tell "the peer has it" from "the
 * peer is offline" from "the server dropped it on the floor" — and drew the same
 * state for all three. [devices] is how many of the recipient's live devices the
 * ciphertext reached; [queued] says the rest was stored for later.
 */
@Serializable
data class SecretAck(
    @ProtoNumber(1) @SerialName("to_user_id") val toUserId: String = "",
    @ProtoNumber(2) @SerialName("to_device_id") val toDeviceId: String = "",
    @ProtoNumber(3) val devices: Int = 0,
    @ProtoNumber(4) val queued: Boolean = false,
)

/**
 * Asks for the ciphertext this device missed while it was away.
 *
 * [after] is the queue id of the last envelope stored, so an interrupted sync resumes
 * rather than restarting.
 */
@Serializable
data class SecretSync(
    @ProtoNumber(1) val after: String = "",
    @ProtoNumber(2) val limit: Int = 0,
)

/**
 * Ends one sync page. [done] false means there is more behind [nextAfter] — a device
 * that has been away a fortnight pages rather than receiving an unbounded burst.
 */
@Serializable
data class SecretSynced(
    @ProtoNumber(1) val count: Int = 0,
    @ProtoNumber(2) @SerialName("next_after") val nextAfter: String = "",
    @ProtoNumber(3) val done: Boolean = false,
)

/**
 * Confirms envelopes are stored on this device and may be dropped.
 *
 * The queue is at-least-once on purpose: deleting on send would lose the message when
 * the connection dies between the write and the client persisting it — the same
 * failure the queue exists to fix, moved one step later.
 */
@Serializable
data class SecretAcked(
    @ProtoNumber(1) val ids: List<String> = emptyList(),
)

// --- Reactions ---

@Serializable
data class React(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(3) val emoji: String = "",
)

/**
 * A reaction change, broadcast to the chat.
 *
 * [counts] is the whole tally rather than a delta: a client that missed one
 * update would otherwise drift, with no way to notice that it had.
 */
@Serializable
data class ReactUpdate(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(3) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(4) val emoji: String = "",
    @ProtoNumber(5) val added: Boolean = false,
    @ProtoNumber(6) val counts: Map<String, Int> = emptyMap(),
)

// --- Threads ---

@Serializable
data class Thread(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("root_id") val rootId: String = "",
    @ProtoNumber(3) @SerialName("after_seq") val afterSeq: Long = 0,
    @ProtoNumber(4) val limit: Int = 0,
)

/** Terminates a streamed thread page; the replies themselves arrive as NEW. */
@Serializable
data class ThreadOk(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("root_id") val rootId: String = "",
    @ProtoNumber(3) @SerialName("next_after") val nextAfter: Long = 0,
    @ProtoNumber(4) val done: Boolean = false,
)

// --- Chat export ---

@Serializable
data class ChatExport(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
)

@Serializable
data class ChatMember(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) val role: String = "",
    @ProtoNumber(3) @SerialName("joined_at") val joinedAt: Long = 0,
)

@Serializable
data class ChatExportResult(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val type: String = "",
    @ProtoNumber(3) val title: String = "",
    @ProtoNumber(4) @SerialName("owner_id") val ownerId: String = "",
    @ProtoNumber(5) val members: List<ChatMember> = emptyList(),
    @ProtoNumber(6) val messages: List<NewMessage> = emptyList(),
    /** True on the final page of a streamed export. */
    @ProtoNumber(7) val done: Boolean = false,
)

// --- Calls (signalling only; media is peer-to-peer and never reaches the server) ---

@Serializable
data class CallInvite(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val kind: String = "",
)

/** ACCEPT, DECLINE and HANGUP all carry only the call id. */
@Serializable
data class CallAction(
    @ProtoNumber(1) @SerialName("call_id") val callId: String = "",
)

@Serializable
data class CallParticipant(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) @SerialName("device_id") val deviceId: String = "",
    @ProtoNumber(3) val state: String = "",
)

@Serializable
data class CallState(
    @ProtoNumber(1) @SerialName("call_id") val callId: String = "",
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) @SerialName("initiator_id") val initiatorId: String = "",
    @ProtoNumber(4) val kind: String = "",
    @ProtoNumber(5) val state: String = "",
    @ProtoNumber(6) val participants: List<CallParticipant> = emptyList(),
)

/** SDP and ICE, relayed verbatim. The server does not parse [payload]. */
@Serializable
data class CallSignal(
    @ProtoNumber(1) @SerialName("call_id") val callId: String = "",
    @ProtoNumber(2) @SerialName("to_user_id") val toUserId: String = "",
    @ProtoNumber(3) @SerialName("to_device_id") val toDeviceId: String = "",
    @ProtoNumber(4) @SerialName("from_user_id") val fromUserId: String = "",
    @ProtoNumber(5) @SerialName("from_device_id") val fromDeviceId: String = "",
    @ProtoNumber(6) @SerialName("signal_type") val signalType: String = "",
    @ProtoNumber(7) val payload: String = "",
)

// --- Polls ---

@Serializable
data class PollCreate(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val question: String = "",
    @ProtoNumber(3) val options: List<String> = emptyList(),
    @ProtoNumber(4) @SerialName("multi_choice") val multiChoice: Boolean = false,
    @ProtoNumber(5) val anonymous: Boolean = false,
)

@Serializable
data class PollVote(
    @ProtoNumber(1) @SerialName("poll_id") val pollId: String = "",
    @ProtoNumber(2) val option: Int = 0,
)

@Serializable
data class PollClose(
    @ProtoNumber(1) @SerialName("poll_id") val pollId: String = "",
)

@Serializable
data class PollOption(
    @ProtoNumber(1) val index: Int = 0,
    @ProtoNumber(2) val text: String = "",
    @ProtoNumber(3) val votes: Int = 0,
)

@Serializable
data class PollState(
    @ProtoNumber(1) @SerialName("poll_id") val pollId: String = "",
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(4) val question: String = "",
    @ProtoNumber(5) val options: List<PollOption> = emptyList(),
    @ProtoNumber(6) @SerialName("total_votes") val totalVotes: Int = 0,
    @ProtoNumber(7) @SerialName("multi_choice") val multiChoice: Boolean = false,
    @ProtoNumber(8) val anonymous: Boolean = false,
    @ProtoNumber(9) val closed: Boolean = false,
    @ProtoNumber(10) @SerialName("my_votes") val myVotes: List<Int> = emptyList(),
)

// --- Forwarding ---

@Serializable
data class Forward(
    @ProtoNumber(1) @SerialName("from_chat_id") val fromChatId: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(3) @SerialName("to_chat_id") val toChatId: String = "",
    @ProtoNumber(4) @SerialName("dedup_key") val dedupKey: String = "",
)

// --- Scheduled sending ---

@Serializable
data class Schedule(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val text: String = "",
    @ProtoNumber(3) @SerialName("media_ref") val mediaRef: String = "",
    @ProtoNumber(4) val attachment: Attachment? = null,
    @ProtoNumber(5) @SerialName("reply_to") val replyTo: String = "",
    @ProtoNumber(6) @SerialName("ttl_seconds") val ttlSeconds: Int = 0,
    @ProtoNumber(7) @SerialName("send_at") val sendAt: Long = 0,
)

@Serializable
data class ScheduleList(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
)

@Serializable
data class ScheduleCancel(
    @ProtoNumber(1) val id: String = "",
)

@Serializable
data class ScheduledItem(
    @ProtoNumber(1) val id: String = "",
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) val text: String = "",
    @ProtoNumber(4) @SerialName("send_at") val sendAt: Long = 0,
)

@Serializable
data class Scheduled(
    @ProtoNumber(1) val items: List<ScheduledItem> = emptyList(),
)

// --- Pins ---

@Serializable
data class Pin(
    @ProtoNumber(1) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(2) @SerialName("pinned_by") val pinnedBy: String = "",
    @ProtoNumber(3) @SerialName("pinned_at") val pinnedAt: Long = 0,
)

/** PIN, UNPIN and PIN_LIST all send this; PIN_LIST leaves [messageId] empty. */
@Serializable
data class PinAction(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("message_id") val messageId: String = "",
)

/** The complete pin set for a chat, never a delta. */
@Serializable
data class Pinned(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val pins: List<Pin> = emptyList(),
)

// --- Drafts ---

@Serializable
data class Draft(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val text: String = "",
    @ProtoNumber(3) @SerialName("reply_to") val replyTo: String = "",
)

@Serializable
data class DraftSync(
    @ProtoNumber(1) val since: Long = 0,
)

@Serializable
data class DraftItem(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val text: String = "",
    @ProtoNumber(3) @SerialName("reply_to") val replyTo: String = "",
    @ProtoNumber(4) @SerialName("updated_at") val updatedAt: Long = 0,
)

@Serializable
data class Drafts(
    @ProtoNumber(1) val drafts: List<DraftItem> = emptyList(),
    @ProtoNumber(2) val cursor: Long = 0,
)

// --- Public handles, invite links, roles ---

@Serializable
data class SetUsername(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val username: String = "",
)

@Serializable
data class InviteCreate(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("expires_at") val expiresAt: Long = 0,
    @ProtoNumber(3) @SerialName("max_uses") val maxUses: Int = 0,
)

@Serializable
data class InviteRevoke(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) val code: String = "",
)

@Serializable
data class InviteList(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
)

@Serializable
data class SetRole(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(3) val role: String = "",
)

// --- Account deletion ---

/**
 * Erases the account.
 *
 * The password is re-checked even though this socket is already authenticated:
 * a session token lives on the device, and someone holding an unlocked phone
 * must not be able to destroy the account behind it with two taps.
 */
@Serializable
data class AccountDelete(
    @ProtoNumber(1) val password: String = "",
    /** Free text. It goes to the audit log and to nothing that survives. */
    @ProtoNumber(2) val reason: String = "",
)

/** The last frame this connection will ever carry: every session is gone. */
@Serializable
data class AccountDeleted(
    @ProtoNumber(1) @SerialName("user_id") val userId: String = "",
    @ProtoNumber(2) @SerialName("deleted_at") val deletedAt: Long = 0,
)

// --- Session management ---

/**
 * One live session.
 *
 * Carries no token: the point is to let a person recognise a device, not to
 * hand this device another one's credentials.
 */
@Serializable
data class SessionInfo(
    @ProtoNumber(1) @SerialName("session_id") val sessionId: String = "",
    @ProtoNumber(2) @SerialName("device_id") val deviceId: String = "",
    @ProtoNumber(3) val platform: String = "",
    @ProtoNumber(4) @SerialName("created_at") val createdAt: Long = 0,
    @ProtoNumber(5) @SerialName("expires_at") val expiresAt: Long = 0,
    /** The session this connection is authenticated with. */
    @ProtoNumber(6) val current: Boolean = false,
)

@Serializable
data class Sessions(
    @ProtoNumber(1) val sessions: List<SessionInfo> = emptyList(),
)

/**
 * Kills one session, or a sweep.
 *
 * An empty [sessionId] means "every session except this one" — what a person
 * reaches for after losing a device. [allIncludingCurrent] extends the sweep to
 * this connection as well; it is a separate field because "log out everywhere"
 * and "log out everywhere but here" are different intentions, and a client
 * should not express the difference by omitting something.
 */
@Serializable
data class SessionRevoke(
    @ProtoNumber(1) @SerialName("session_id") val sessionId: String = "",
    @ProtoNumber(2) @SerialName("all_including_current") val allIncludingCurrent: Boolean = false,
)

/** What the revoke actually did. The count matters, and so does [self]. */
@Serializable
data class SessionRevoked(
    @ProtoNumber(1) val revoked: Int = 0,
    /** True when this connection's own session was among them. */
    @ProtoNumber(2) val self: Boolean = false,
)

// --- Batched history ---

/**
 * One backfill page in a single frame.
 *
 * HISTORY used to answer with up to a hundred separate NEW frames — a hundred
 * envelopes and a hundred trips through the connection's single writer for one
 * request. Sent only to peers that negotiated batching in HELLO.
 */
@Serializable
data class HistoryPage(
    @ProtoNumber(1) val messages: List<NewMessage> = emptyList(),
    /** The RESOLVED chat id, even when the request addressed `@handle`. */
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) @SerialName("next_before") val nextBefore: Long = 0,
    @ProtoNumber(4) val done: Boolean = false,
)

// --- Privacy ---

/**
 * Who may see what.
 *
 * Each value is `"everyone"`, `"contacts"` or `"nobody"`. Strings rather than an
 * enum, deliberately: a client that receives an unfamiliar value can render it
 * as "something stricter than I know about" instead of decoding it as zero.
 *
 * PRIVACY_SET replaces all three every time — a partial update would make
 * "nobody" indistinguishable from "unset". PRIVACY_GET carries no body: these
 * settings are not readable for anyone else, and a field that could name a
 * target would leak exactly what they exist to protect.
 */
@Serializable
data class Privacy(
    @ProtoNumber(1) @SerialName("last_seen") val lastSeen: String = "",
    @ProtoNumber(2) val avatar: String = "",
    @ProtoNumber(3) val groups: String = "",
    /**
     * Whether message TEXT may be sent to the push provider.
     *
     * The audience for this one is not another user but Apple and Google: the
     * notification path used to include a preview of every message unconditionally, so
     * a third party saw the contents of every conversation — a wider disclosure than
     * anything end-to-end encryption was protecting against, since E2E guards against
     * the server and this was the server volunteering the text.
     *
     * Defaults false. It is the one setting whose default deliberately does NOT
     * preserve the previous behaviour, because that behaviour was a leak rather than a
     * preference anyone chose.
     */
    @ProtoNumber(4) @SerialName("push_preview") val pushPreview: Boolean = false,
)

// --- Per-member chat settings ---

/**
 * This account's own settings for one chat.
 *
 * `muted` existed in the server schema from its first migration with nothing reading
 * it and no message to set it, so muting a chat was impossible while looking supported
 * from every other angle.
 *
 * [mutedUntil] is a DEADLINE in unix millis, not a flag: "mute for eight hours" is
 * what muting usually means and a boolean cannot express it, while a distant deadline
 * expresses "forever". 0 unmutes.
 *
 * All three fields are sent every time — the server REPLACES rather than patches — so
 * a caller changing the pin has to send the mute it already has, or drop it.
 */
@Serializable
data class ChatFlags(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("muted_until") val mutedUntil: Long = 0,
    @ProtoNumber(3) val pinned: Boolean = false,
    @ProtoNumber(4) val archived: Boolean = false,
)

/**
 * The flags now STORED, which is not always what was asked for: the server normalises
 * a mute deadline that has already passed to zero. A client that kept its own request
 * would show a chat as muted until something contradicted it.
 */
@Serializable
data class ChatFlagsSet(
    @ProtoNumber(1) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(2) @SerialName("muted_until") val mutedUntil: Long = 0,
    @ProtoNumber(3) val pinned: Boolean = false,
    @ProtoNumber(4) val archived: Boolean = false,
)

// --- Account security ---
//
// None of this existed. The password could not be CHANGED by any path — so a leaked
// one meant a permanently lost account, since revoking every session does not stop
// whoever knows the password from signing in again — and there was no second factor
// at all, leaving one interceptable credential and nothing behind it.

/**
 * Replaces the password.
 *
 * The old one is required even on an authenticated connection: a session token is
 * enough to ACT as the account but not to replace its credential, or a stolen token
 * becomes permanent ownership — which is exactly what a password change is meant to
 * take back.
 */
@Serializable
data class PasswordChange(
    @ProtoNumber(1) @SerialName("old_password") val oldPassword: String = "",
    @ProtoNumber(2) @SerialName("new_password") val newPassword: String = "",
)

/**
 * How many OTHER sessions were signed out. The caller's own survives, so they are not
 * logged out of the device they are using to secure the account.
 */
@Serializable
data class PasswordChanged(
    @ProtoNumber(1) @SerialName("sessions_revoked") val sessionsRevoked: Int = 0,
)

/**
 * Carries the new TOTP secret and the `otpauth://` URI to render as a QR code.
 *
 * The factor is NOT yet enforced at this point. Enrolment is two steps on purpose:
 * without a confirmation step a mis-scanned QR code locks an account out of itself,
 * which is the most common way a 2FA rollout goes wrong.
 */
@Serializable
data class TOTPSetupInfo(
    @ProtoNumber(1) val secret: String = "",
    @ProtoNumber(2) val uri: String = "",
)

/** Proves the user can produce a code, which is what enrols them. */
@Serializable
data class TOTPConfirm(
    @ProtoNumber(1) val code: String = "",
)

/**
 * Removes the factor. Password AND code, because someone holding only a stolen session
 * token must not be able to take off the factor that would keep them out of the next
 * login.
 */
@Serializable
data class TOTPDisable(
    @ProtoNumber(1) val password: String = "",
    @ProtoNumber(2) val code: String = "",
)

/**
 * The current second-factor state.
 *
 * [recoveryCodes] is populated ONLY in the reply to TOTP_CONFIRM. The stored form is an
 * argon2id hash, so there is nothing to show later — which is what makes a leak of that
 * table worthless, and the reason a client has to put them in front of the user
 * immediately rather than logging them and moving on.
 *
 * [recoveryLeft] is exposed because losing the last code and the phone together is the
 * state there is no way back from, and a screen that cannot see the count cannot warn
 * before it happens.
 */
@Serializable
data class TOTPState(
    @ProtoNumber(1) val enabled: Boolean = false,
    @ProtoNumber(2) @SerialName("recovery_left") val recoveryLeft: Int = 0,
    @ProtoNumber(3) @SerialName("recovery_codes") val recoveryCodes: List<String> = emptyList(),
    @ProtoNumber(4) @SerialName("confirmed_at_ms") val confirmedAtMs: Long = 0,
)

// --- Billing ---

/**
 * Asks what is purchasable.
 *
 * [country] comes from the CLIENT rather than a server-side GeoIP lookup: the user
 * knows which market they are in, and a lookup that guesses wrong offers a payment
 * method their bank does not support.
 */
@Serializable
data class BillingPlans(
    @ProtoNumber(1) val country: String = "",
)

@Serializable
data class BillingOffers(
    @ProtoNumber(1) val offers: List<PlanOffer> = emptyList(),
)

/**
 * One purchasable plan in one market.
 *
 * [amountMinor] is an INTEGER in the currency's minor unit — kopeks, cents. Never a
 * float and never a pre-formatted string: a price is an exact quantity, binary floating
 * point cannot hold 0.01, and a client that renders the price through a float
 * eventually shows a number that differs from what it charges.
 */
@Serializable
data class PlanOffer(
    @ProtoNumber(1) val plan: String = "",
    @ProtoNumber(2) @SerialName("amount_minor") val amountMinor: Long = 0,
    @ProtoNumber(3) val currency: String = "",
    @ProtoNumber(4) @SerialName("period_days") val periodDays: Int = 0,
    @ProtoNumber(5) val methods: List<String> = emptyList(),
)

/** Starts a payment. */
@Serializable
data class BillingCheckout(
    @ProtoNumber(1) val plan: String = "",
    @ProtoNumber(2) val method: String = "",
    /**
     * REQUIRED, and the client's. A retried checkout must reach the same payment rather
     * than starting a second one, and only the client knows two requests are the same
     * request — a key the server invents differs on every retry, which is the same as
     * having none. The thing being duplicated otherwise is somebody's money.
     */
    @ProtoNumber(3) @SerialName("idempotency_key") val idempotencyKey: String = "",
    @ProtoNumber(4) val country: String = "",
    @ProtoNumber(5) @SerialName("return_url") val returnUrl: String = "",
)

/**
 * Where to send the user.
 *
 * [payUrl] is FOLLOWED; [qrPayload] is DISPLAYED as a QR code. They are not
 * interchangeable — an SBP payload is not a URL, and a client that renders it as a link
 * produces a broken one.
 */
@Serializable
data class BillingPayment(
    @ProtoNumber(1) @SerialName("payment_id") val paymentId: String = "",
    @ProtoNumber(2) val status: String = "",
    @ProtoNumber(3) @SerialName("amount_minor") val amountMinor: Long = 0,
    @ProtoNumber(4) val currency: String = "",
    @ProtoNumber(5) @SerialName("pay_url") val payUrl: String = "",
    @ProtoNumber(6) @SerialName("qr_payload") val qrPayload: String = "",
    /** True when this repeated an earlier request and no new charge was made. */
    @ProtoNumber(7) val deduplicated: Boolean = false,
)

/**
 * The caller's tier and what it grants.
 *
 * Sent as a reply to BILLING_STATUS and PUSHED whenever the subscription changes — a
 * payment settling, a cancellation, a period lapsing. The push is what keeps a client
 * from offering features the server has started refusing, which a user experiences as
 * the app breaking rather than as a plan ending.
 *
 * The entitlements are sent EXPLICITLY rather than derived from the plan name. A client
 * that mapped "premium" to a set of capabilities would hold a second copy of the
 * policy, and the two drift: a server raising the upload ceiling would need every
 * client updated before anybody could use it.
 */
@Serializable
data class Subscription(
    @ProtoNumber(1) val plan: String = "",
    @ProtoNumber(2) val status: String = "",
    /** When access lapses without a renewal, unix millis; 0 = no expiry. */
    @ProtoNumber(3) @SerialName("period_end") val periodEnd: Long = 0,
    @ProtoNumber(4) @SerialName("cancel_at_period_end") val cancelAtPeriodEnd: Boolean = false,
    @ProtoNumber(5) @SerialName("secret_chats") val secretChats: Boolean = false,
    @ProtoNumber(6) @SerialName("max_upload_bytes") val maxUploadBytes: Long = 0,
    @ProtoNumber(7) @SerialName("max_pinned_chats") val maxPinnedChats: Int = 0,
    @ProtoNumber(8) val folders: Boolean = false,
    @ProtoNumber(9) @SerialName("advanced_search") val advancedSearch: Boolean = false,
    @ProtoNumber(10) @SerialName("priority_delivery") val priorityDelivery: Boolean = false,
    @ProtoNumber(11) @SerialName("voice_transcription") val voiceTranscription: Boolean = false,
    @ProtoNumber(12) val badge: Boolean = false,
)
