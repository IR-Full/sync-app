package com.syncapp.messenger.domain.model

/**
 * Domain models. Immutable, protocol-free: nothing here knows about envelopes,
 * protobuf or Room. The wire shapes live in `network/protocol` and are translated
 * by the mappers in `data/mapper`.
 */

enum class ChatKind {
    DIRECT,
    GROUP,
    CHANNEL,

    /**
     * End-to-end encrypted, two parties, Premium-gated.
     *
     * A chat TYPE rather than a mode, which is the whole design: it is created, listed,
     * opened and read like any other chat, and the only difference the user sees is a lock
     * badge. Making it a separate surface — the bottom sheet it used to be — meant secret
     * conversations had no history, no unread count and no place in the list, so nobody
     * used them twice.
     */
    SECRET,
    UNKNOWN,
    ;

    /**
     * Exactly two participants, so the row is named after the peer rather than a title.
     *
     * Both [DIRECT] and [SECRET] qualify, and forgetting the second is why a secret chat
     * rendered with a blank name and no presence dot.
     */
    val isTwoParty: Boolean get() = this == DIRECT || this == SECRET

    /** The messages never leave the devices in plaintext. */
    val isEndToEnd: Boolean get() = this == SECRET
}

/**
 * This account's own settings for one chat.
 *
 * All three travel together because the wire carries no field presence: proto3 cannot
 * express "leave pinned alone", so every write states all three and a caller that
 * defaulted the ones it did not care about would silently clear them.
 */
data class ChatFlags(
    /** When notifications resume, unix millis. 0 = not muted. */
    val mutedUntil: Long = 0,
    val pinned: Boolean = false,
    val archived: Boolean = false,
) {
    /**
     * Whether notifications are suppressed right NOW.
     *
     * Derived from the deadline rather than stored, so a mute expires on its own without
     * anything having to run at the moment it does.
     */
    fun isMuted(nowMs: Long = System.currentTimeMillis()): Boolean =
        mutedUntil > nowMs
}

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
    val flags: ChatFlags = ChatFlags(),
    /**
     * The sort key: the newest message, falling back to the chat's creation time.
     *
     * Separate from [lastMessage] because of that fallback — an empty chat still has a
     * position, and it is at the top rather than the bottom.
     */
    val lastActivityAt: Long = 0,
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

/**
 * One live session of this account, as the server sees it.
 *
 * Carries no token. The point of showing these is to let a person recognise a
 * device and decide to end it, not to hand this device another one's
 * credentials — a list that included them would turn "review my sessions" into
 * the most dangerous screen in the app.
 */
data class DeviceSession(
    val sessionId: String,
    val deviceId: String,
    val platform: String,
    val createdAtMs: Long,
    val expiresAtMs: Long,
    /** The session this connection is authenticated with. */
    val current: Boolean,
)

/**
 * Who may see something.
 *
 * Three values rather than a boolean, matching the server: "contacts" is the
 * setting most people actually want and neither of the other two expresses it.
 * [UNKNOWN] exists because the wire format is a string and a future server may
 * send a value this build has never heard of — rendering that as "stricter than
 * I know about" is honest, and decoding it as EVERYONE would be a privacy
 * failure introduced by an upgrade.
 */
enum class Visibility {
    EVERYONE,
    CONTACTS,
    NOBODY,
    UNKNOWN;

    /** The wire spelling. [UNKNOWN] never leaves the device, so it sends as-is. */
    val wire: String
        get() = when (this) {
            EVERYONE -> "everyone"
            CONTACTS -> "contacts"
            NOBODY -> "nobody"
            UNKNOWN -> "nobody"
        }

    companion object {
        fun fromWire(value: String): Visibility = when (value.lowercase()) {
            "everyone" -> EVERYONE
            "contacts" -> CONTACTS
            "nobody" -> NOBODY
            else -> UNKNOWN
        }
    }
}

/**
 * The account's privacy settings.
 *
 * Three settings rather than one level, because they are read on different paths
 * and answer different questions: presence fanout asks about last seen, a
 * profile read asks about the avatar, and a group add asks whether the actor may
 * add this user at all.
 */
data class PrivacySettings(
    val lastSeen: Visibility = Visibility.EVERYONE,
    val avatar: Visibility = Visibility.EVERYONE,
    val groups: Visibility = Visibility.EVERYONE,
)
