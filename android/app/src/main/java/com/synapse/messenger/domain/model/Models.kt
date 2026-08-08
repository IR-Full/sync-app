package com.synapse.messenger.domain.model

/**
 * Domain models. Immutable, protocol-free: nothing here knows about envelopes,
 * protobuf or Room. The wire shapes live in `network/protocol` and are translated
 * by the mappers in `data/mapper`.
 */

enum class ChatKind { DIRECT, GROUP, CHANNEL, UNKNOWN }

/**
 * Delivery state of an outgoing message. Every step is sourced from a distinct
 * server fact, none inferred:
 *
 *  - [SENT] — SEND_ACK: the write is durable.
 *  - [DELIVERED] — DELIVERED: the gateway holding a recipient's socket wrote the
 *    frame to it. Reported by the node that witnessed the write, so it means the
 *    bytes left the server rather than that a node was notified.
 *  - [READ] — READ_UPD: their read cursor passed this message.
 */
enum class MessageStatus { PENDING, SENT, DELIVERED, READ, FAILED }

enum class AttachmentKind {
    IMAGE,
    VIDEO,
    VOICE,
    VIDEO_NOTE,
    FILE,
    ;

    companion object {
        fun fromWire(kind: String): AttachmentKind = when (kind.lowercase()) {
            "image" -> IMAGE
            "video" -> VIDEO
            "voice" -> VOICE
            "video_note" -> VIDEO_NOTE
            else -> FILE
        }
    }

    fun toWire(): String = when (this) {
        IMAGE -> "image"
        VIDEO -> "video"
        VOICE -> "voice"
        VIDEO_NOTE -> "video_note"
        FILE -> "file"
    }
}

data class MessageAttachment(
    val kind: AttachmentKind,
    val mediaRef: String,
    val filename: String = "",
    val mime: String = "",
    val size: Long = 0,
    val durationMs: Long = 0,
    val width: Int = 0,
    val height: Int = 0,
    val thumbRef: String = "",
    val waveform: List<Int> = emptyList(),
)

data class ForwardedFrom(
    val chatId: String,
    val messageId: String,
    val senderId: String,
)

data class Message(
    val id: String,
    val chatId: String,
    val senderId: String,
    /** Server per-chat ordering position; 0 while the message is still unsent. */
    val seq: Long,
    val text: String,
    val attachment: MessageAttachment? = null,
    val replyTo: String? = null,
    val forwardedFrom: ForwardedFrom? = null,
    val edited: Boolean = false,
    val deleted: Boolean = false,
    val createdAt: Long,
    val status: MessageStatus,
    val isOutgoing: Boolean,
    /** Self-destruct deadline in unix millis, 0 when the message does not expire. */
    val expiresAt: Long = 0,
)

data class LastMessage(
    val text: String,
    val senderId: String,
    val seq: Long,
    val timestamp: Long,
)

data class Chat(
    val id: String,
    val kind: ChatKind,
    val title: String,
    val peerUserId: String? = null,
    val peerUsername: String? = null,
    /** Media reference for the peer's avatar, in a direct chat. */
    val peerAvatarRef: String? = null,
    val lastMessage: LastMessage? = null,
    val unreadCount: Int = 0,
    val myReadSeq: Long = 0,
    val oldestLoadedSeq: Long = 0,
    val hasMoreHistory: Boolean = true,
)

/**
 * Someone we can address. [username] is only ever known because this device
 * recorded it: the protocol resolves `"@name"` to an id but never sends a name
 * back.
 */
data class UserSummary(
    val userId: String,
    val username: String? = null,
    /** Our private address-book label for them. */
    val name: String? = null,
    /** Their own profile name. */
    val displayName: String? = null,
    /** Media reference for their avatar. */
    val avatarRef: String? = null,
    val isContact: Boolean = false,
    val blocked: Boolean = false,
) {
    /**
     * What to call them. Our own label wins over their profile name — an address
     * book exists precisely so a person can be filed under the name we know them
     * by — and a handle beats the snowflake id we would otherwise be stuck with.
     */
    val displayLabel: String
        get() = name?.takeIf { it.isNotBlank() }
            ?: displayName?.takeIf { it.isNotBlank() }
            ?: username?.let { "@$it" }
            ?: userId
}

/**
 * Someone's online state. Absent (a null [UserPresence]) means "we have not been
 * told", which is different from offline and must render differently: the gateway
 * only reports presence for people it has sent us a frame about.
 */
data class UserPresence(
    val userId: String,
    val online: Boolean,
    /** When they were last seen, in unix millis. */
    val lastSeenMs: Long,
)

/** The signed-in identity, as AUTH_OK reported it. */
data class Session(
    val userId: String,
    val username: String,
    val deviceId: String,
    val displayName: String = "",
    val avatarRef: String? = null,
)

/**
 * Where a send should go.
 *
 * A direct chat may not exist yet: the gateway creates it when the first message
 * addressed to `"@username"` arrives, and only the SEND_ACK reveals its id. So a
 * target is either a resolved chat or a peer we have never messaged — and both
 * must work offline, which is why the unresolved form is a first-class value
 * rather than a null chat id.
 */
sealed interface ChatTarget {
    val ref: String

    data class Existing(val chatId: String) : ChatTarget {
        override val ref: String get() = chatId
    }

    data class DirectPeer(val username: String) : ChatTarget {
        override val ref: String get() = "@${username.removePrefix("@").lowercase()}"
    }
}
