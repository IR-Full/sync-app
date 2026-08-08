package com.synapse.messenger.data.mapper

import com.synapse.messenger.database.dao.ChatListRow
import com.synapse.messenger.database.entity.ChatEntity
import com.synapse.messenger.database.entity.MessageEntity
import com.synapse.messenger.database.entity.MessageStatuses
import com.synapse.messenger.database.entity.StoredAttachment
import com.synapse.messenger.database.entity.UserEntity
import com.synapse.messenger.domain.model.AttachmentKind
import com.synapse.messenger.domain.model.Chat
import com.synapse.messenger.domain.model.ChatKind
import com.synapse.messenger.domain.model.ForwardedFrom
import com.synapse.messenger.domain.model.LastMessage
import com.synapse.messenger.domain.model.Message
import com.synapse.messenger.domain.model.MessageAttachment
import com.synapse.messenger.domain.model.MessageStatus
import com.synapse.messenger.domain.model.UserSummary
import com.synapse.messenger.network.protocol.Attachment as WireAttachment
import com.synapse.messenger.network.protocol.Profile as WireProfile
import com.synapse.messenger.network.protocol.NewMessage

/**
 * Wire ↔ storage ↔ domain translation. Three shapes on purpose: protobuf bodies
 * evolve with the server, the database schema evolves with migrations, and the
 * domain evolves with the product. Collapsing them would tie a UI change to a
 * schema change.
 */

// --- wire → storage ---

fun NewMessage.toEntity(status: String = MessageStatuses.SENT): MessageEntity = MessageEntity(
    messageId = messageId,
    chatId = chatId,
    senderId = senderId,
    seq = chatSeq,
    text = text,
    mediaRef = mediaRef.takeIf { it.isNotEmpty() },
    attachmentJson = StoredAttachment.encode(attachment?.toStored()),
    replyTo = replyTo.takeIf { it.isNotEmpty() },
    forwardChatId = forward?.chatId?.takeIf { it.isNotEmpty() },
    forwardMessageId = forward?.messageId?.takeIf { it.isNotEmpty() },
    forwardSenderId = forward?.senderId?.takeIf { it.isNotEmpty() },
    threadRoot = threadRoot.takeIf { it.isNotEmpty() },
    replyCount = replyCount,
    expiresAt = expiresAt,
    edited = edited,
    deleted = deleted,
    createdAt = timestamp,
    status = status,
)

fun WireAttachment.toStored(): StoredAttachment = StoredAttachment(
    kind = kind,
    mediaRef = mediaRef,
    filename = filename,
    mime = mime,
    size = size,
    durationMs = durationMs,
    width = width,
    height = height,
    thumbRef = thumbRef,
    waveform = waveform,
)

fun MessageAttachment.toWire(): WireAttachment = WireAttachment(
    kind = kind.toWire(),
    mediaRef = mediaRef,
    filename = filename,
    mime = mime,
    size = size,
    durationMs = durationMs,
    waveform = waveform,
    width = width,
    height = height,
    thumbRef = thumbRef,
)

fun MessageAttachment.toStored(): StoredAttachment = StoredAttachment(
    kind = kind.toWire(),
    mediaRef = mediaRef,
    filename = filename,
    mime = mime,
    size = size,
    durationMs = durationMs,
    width = width,
    height = height,
    thumbRef = thumbRef,
    waveform = waveform,
)

// --- storage → domain ---

fun MessageEntity.toDomain(selfId: String): Message = Message(
    id = messageId,
    chatId = chatId,
    senderId = senderId,
    seq = seq,
    text = text,
    attachment = StoredAttachment.decode(attachmentJson)?.toDomain(),
    replyTo = replyTo,
    forwardedFrom = forwardChatId?.let {
        ForwardedFrom(
            chatId = it,
            messageId = forwardMessageId.orEmpty(),
            senderId = forwardSenderId.orEmpty(),
        )
    },
    edited = edited,
    deleted = deleted,
    createdAt = createdAt,
    status = statusToDomain(status),
    isOutgoing = senderId == selfId,
    expiresAt = expiresAt,
)

fun StoredAttachment.toDomain(): MessageAttachment = MessageAttachment(
    kind = AttachmentKind.fromWire(kind),
    mediaRef = mediaRef,
    filename = filename,
    mime = mime,
    size = size,
    durationMs = durationMs,
    width = width,
    height = height,
    thumbRef = thumbRef,
    waveform = waveform,
)

fun statusToDomain(raw: String): MessageStatus = when (raw) {
    MessageStatuses.PENDING -> MessageStatus.PENDING
    MessageStatuses.DELIVERED -> MessageStatus.DELIVERED
    MessageStatuses.READ -> MessageStatus.READ
    MessageStatuses.FAILED -> MessageStatus.FAILED
    else -> MessageStatus.SENT
}

/**
 * [resolveUser] looks a person up in the local directory. Passed in rather than
 * queried here because a list of chats needs one directory read, not one per row.
 */
fun ChatListRow.toDomain(resolveUser: (String) -> UserSummary?): Chat {
    // A chat-list row from the server names a direct chat's peer outright. The
    // fallback is for chats learned from an incoming message before the first list
    // sync: a NEW frame carries no type, title or membership, so when exactly one
    // other person has written in it, that person IS the conversation.
    val inferredPeer = otherSenderId?.takeIf { otherSenderCount <= 1 }
    val peerId = chat.peerUserId ?: inferredPeer
    val peer = peerId?.let(resolveUser)
    val base = chat.toDomain(unreadCount)
    return base.copy(
        title = chat.title.ifEmpty { peer?.displayLabel.orEmpty() },
        peerUserId = peerId,
        peerAvatarRef = peer?.avatarRef,
    )
}

fun ChatEntity.toDomain(unreadCount: Int = 0): Chat = Chat(
    id = chatId,
    kind = kindOf(type),
    title = title,
    peerUserId = peerUserId,
    peerUsername = peerUsername,
    lastMessage = lastMessageAt.takeIf { it > 0 }?.let {
        LastMessage(
            text = lastMessageText.orEmpty(),
            senderId = lastMessageSenderId.orEmpty(),
            seq = lastMessageSeq,
            timestamp = it,
        )
    },
    unreadCount = unreadCount,
    myReadSeq = myReadSeq,
    oldestLoadedSeq = oldestLoadedSeq,
    hasMoreHistory = hasMoreHistory,
)

fun kindOf(type: String): ChatKind = when (type.lowercase()) {
    "direct" -> ChatKind.DIRECT
    "group" -> ChatKind.GROUP
    "channel" -> ChatKind.CHANNEL
    else -> ChatKind.UNKNOWN
}

fun ChatKind.toWire(): String = when (this) {
    ChatKind.DIRECT -> "direct"
    ChatKind.GROUP -> "group"
    ChatKind.CHANNEL -> "channel"
    ChatKind.UNKNOWN -> "group"
}

fun UserEntity.toDomain(): UserSummary = UserSummary(
    userId = userId,
    username = username,
    name = name,
    displayName = displayName,
    avatarRef = avatarRef,
    isContact = isContact,
    blocked = blocked,
)

fun WireProfile.toDomain(): UserSummary = UserSummary(
    userId = userId,
    username = username.takeIf { it.isNotEmpty() },
    displayName = displayName.takeIf { it.isNotEmpty() },
    avatarRef = avatarRef.takeIf { it.isNotEmpty() },
)
