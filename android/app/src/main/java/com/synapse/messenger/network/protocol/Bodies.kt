package com.synapse.messenger.network.protocol

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.protobuf.ProtoNumber

/**
 * Envelope bodies, hand-mapped from `server/proto/synapse/v1/body.proto`.
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
)

@Serializable
data class SearchHit(
    @ProtoNumber(1) @SerialName("message_id") val messageId: String = "",
    @ProtoNumber(2) @SerialName("chat_id") val chatId: String = "",
    @ProtoNumber(3) @SerialName("sender_id") val senderId: String = "",
    @ProtoNumber(4) val seq: Long = 0,
    @ProtoNumber(5) val text: String = "",
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

/** Keyset pagination by chat id: [after] is the last id of the previous page. */
@Serializable
data class ChatList(
    @ProtoNumber(1) val after: String = "",
    @ProtoNumber(2) val limit: Int = 0,
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
)

@Serializable
data class Chats(
    @ProtoNumber(1) val chats: List<ChatSummary> = emptyList(),
    @ProtoNumber(2) @SerialName("next_after") val nextAfter: String = "",
    @ProtoNumber(3) val done: Boolean = false,
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
)
