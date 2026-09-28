package com.syncapp.messenger.data.mapper

import com.syncapp.messenger.database.dao.ChatListRow
import com.syncapp.messenger.database.entity.ChatEntity
import com.syncapp.messenger.database.entity.MessageEntity
import com.syncapp.messenger.database.entity.MessageStatuses
import com.syncapp.messenger.database.entity.StoredAttachment
import com.syncapp.messenger.database.entity.UserEntity
import com.syncapp.messenger.domain.model.AttachmentKind
import com.syncapp.messenger.domain.model.ChatKind
import com.syncapp.messenger.domain.model.MessageAttachment
import com.syncapp.messenger.domain.model.MessageStatus
import com.syncapp.messenger.domain.model.UserSummary
import com.syncapp.messenger.network.protocol.Attachment as WireAttachment
import com.syncapp.messenger.network.protocol.NewMessage
import com.syncapp.messenger.network.protocol.Profile as WireProfile
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Three shapes on purpose — wire, storage, domain — so a protocol change does not
 * force a migration and a UI change does not force either. The cost is that a
 * field can be dropped at any of the two boundaries, and nothing fails: it simply
 * renders blank.
 *
 * These cover the conversions the existing MapperTest does not, and the
 * `takeIf { isNotEmpty() }` idiom in particular. Protobuf has no null, so the wire
 * carries "" for an absent value while the database carries NULL — conflating them
 * writes an empty string into a column the UI then treats as present.
 */
class ConversionsTest {

    // ------------------------------------------------------ attachment round trip

    private val fullAttachment = MessageAttachment(
        kind = AttachmentKind.VOICE,
        mediaRef = "media-1",
        filename = "note.ogg",
        mime = "audio/ogg",
        size = 2048,
        durationMs = 3400,
        width = 0,
        height = 0,
        thumbRef = "thumb-1",
        waveform = listOf(10, 40, 90),
    )

    @Test
    fun `an attachment survives domain to wire and back`() {
        val round = fullAttachment.toWire().toStored().toDomain()

        assertEquals(fullAttachment.kind, round.kind)
        assertEquals(fullAttachment.mediaRef, round.mediaRef)
        assertEquals(fullAttachment.filename, round.filename)
        assertEquals(fullAttachment.mime, round.mime)
        assertEquals(fullAttachment.size, round.size)
        assertEquals(fullAttachment.durationMs, round.durationMs)
        assertEquals(fullAttachment.thumbRef, round.thumbRef)
    }

    @Test
    fun `a waveform survives the round trip`() {
        // A voice note renders its bars from this before the audio is fetched;
        // losing it draws a flat line rather than an error.
        val round = fullAttachment.toWire().toStored().toDomain()
        assertEquals(listOf(10, 40, 90), round.waveform)
    }

    @Test
    fun `an attachment survives domain to storage and back`() {
        val round = fullAttachment.toStored().toDomain()
        assertEquals(fullAttachment.kind, round.kind)
        assertEquals(fullAttachment.mediaRef, round.mediaRef)
        assertEquals(fullAttachment.waveform, round.waveform)
    }

    @Test
    fun `every attachment kind survives the string round trip`() {
        // The kind travels as a plain string on the wire and in the column, so a
        // round trip that lost it would render a voice note as a generic file.
        for (kind in AttachmentKind.entries) {
            assertEquals("kind $kind did not survive", kind, AttachmentKind.fromWire(kind.toWire()))
        }
    }

    @Test
    fun `an unrecognised attachment kind degrades to a file`() {
        // A newer server may introduce a kind this build has never heard of. FILE
        // is the one renderer that works for any blob — a download card — so the
        // message stays usable instead of rendering as nothing.
        assertEquals(AttachmentKind.FILE, AttachmentKind.fromWire("hologram"))
        assertEquals(AttachmentKind.FILE, AttachmentKind.fromWire(""))
    }

    @Test
    fun `attachment kind parsing is case insensitive`() {
        assertEquals(AttachmentKind.VOICE, AttachmentKind.fromWire("VOICE"))
        assertEquals(AttachmentKind.VIDEO_NOTE, AttachmentKind.fromWire("Video_Note"))
    }

    @Test
    fun `an attachment survives a JSON round trip through the column`() {
        // It is stored as JSON in one column; the encode/decode pair is what a
        // reload actually goes through.
        val encoded = StoredAttachment.encode(fullAttachment.toStored())
        val decoded = StoredAttachment.decode(encoded)

        assertEquals("media-1", decoded?.mediaRef)
        assertEquals(listOf(10, 40, 90), decoded?.waveform)
    }

    @Test
    fun `no attachment encodes to null rather than an empty object`() {
        // An empty object would decode back into an attachment and make the bubble
        // render a media card for a plain text message.
        assertNull(StoredAttachment.encode(null))
        assertNull(StoredAttachment.decode(null))
    }

    // ------------------------------------------------- wire emptiness vs null

    @Test
    fun `an absent media ref becomes null, not an empty string`() {
        /*
         * Protobuf has no null: an unset string arrives as "". The column is
         * nullable and the UI checks for null, so storing "" would make every
         * text message look like it carried media.
         */
        val entity = NewMessage(
            messageId = "m1", chatId = "c1", senderId = "u1", chatSeq = 1,
            text = "hello", mediaRef = "", timestamp = 1_000,
        ).toEntity()

        assertNull(entity.mediaRef)
    }

    @Test
    fun `an absent reply target becomes null`() {
        val entity = NewMessage(
            messageId = "m1", chatId = "c1", senderId = "u1", chatSeq = 1,
            replyTo = "", timestamp = 1_000,
        ).toEntity()

        assertNull(entity.replyTo)
    }

    @Test
    fun `an absent thread root becomes null`() {
        // A null thread root means "top level"; an empty string would make the
        // message look like it belonged to a thread rooted at nothing.
        val entity = NewMessage(
            messageId = "m1", chatId = "c1", senderId = "u1", chatSeq = 1,
            threadRoot = "", timestamp = 1_000,
        ).toEntity()

        assertNull(entity.threadRoot)
    }

    @Test
    fun `a populated media ref is kept`() {
        val entity = NewMessage(
            messageId = "m1", chatId = "c1", senderId = "u1", chatSeq = 1,
            mediaRef = "media-1", timestamp = 1_000,
        ).toEntity()

        assertEquals("media-1", entity.mediaRef)
    }

    @Test
    fun `forward provenance is split across its three columns`() {
        val entity = NewMessage(
            messageId = "m1", chatId = "c1", senderId = "u1", chatSeq = 1,
            forward = com.syncapp.messenger.network.protocol.ForwardOrigin(
                chatId = "c0", messageId = "m0", senderId = "u9",
            ),
            timestamp = 1_000,
        ).toEntity()

        assertEquals("c0", entity.forwardChatId)
        assertEquals("m0", entity.forwardMessageId)
        assertEquals("u9", entity.forwardSenderId)
    }

    @Test
    fun `a message that was not forwarded leaves its columns null`() {
        val entity = NewMessage(
            messageId = "m1", chatId = "c1", senderId = "u1", chatSeq = 1, timestamp = 1_000,
        ).toEntity()

        assertNull(entity.forwardChatId)
        assertNull(entity.forwardMessageId)
        assertNull(entity.forwardSenderId)
    }

    @Test
    fun `forward provenance survives storage to domain`() {
        // Without it the copy reads as originally written by whoever forwarded it.
        val message = MessageEntity(
            messageId = "m1", chatId = "c1", senderId = "u1", seq = 1, text = "x",
            forwardChatId = "c0", forwardMessageId = "m0", forwardSenderId = "u9",
            createdAt = 1_000, status = MessageStatuses.SENT,
        ).toDomain(selfId = "self")

        assertEquals("c0", message.forwardedFrom?.chatId)
        assertEquals("u9", message.forwardedFrom?.senderId)
    }

    // ------------------------------------------------------------ message status

    @Test
    fun `every stored status maps to its domain value`() {
        // The tick a user sees is driven by this; an unmapped status would show a
        // delivered tick on a message that failed.
        assertEquals(MessageStatus.PENDING, statusToDomain(MessageStatuses.PENDING))
        assertEquals(MessageStatus.SENT, statusToDomain(MessageStatuses.SENT))
        assertEquals(MessageStatus.DELIVERED, statusToDomain(MessageStatuses.DELIVERED))
        assertEquals(MessageStatus.READ, statusToDomain(MessageStatuses.READ))
        assertEquals(MessageStatus.FAILED, statusToDomain(MessageStatuses.FAILED))
    }

    @Test
    fun `an unrecognised status degrades to sent`() {
        /*
         * A row written by a newer build, or a corrupted column, must not render as
         * PENDING — a spinner on a message that was delivered months ago is worse
         * than an optimistic tick, and the message is in the database precisely
         * because the server accepted it.
         */
        assertEquals(MessageStatus.SENT, statusToDomain("something_new"))
        assertEquals(MessageStatus.SENT, statusToDomain(""))
    }

    @Test
    fun `a message is outgoing only when we sent it`() {
        val mine = MessageEntity(
            messageId = "m1", chatId = "c1", senderId = "self", seq = 1, text = "x",
            createdAt = 1, status = MessageStatuses.SENT,
        ).toDomain(selfId = "self")
        val theirs = mine.copy(senderId = "u2")

        assertTrue(mine.isOutgoing)
        assertFalse(
            MessageEntity(
                messageId = "m2", chatId = "c1", senderId = "u2", seq = 2, text = "x",
                createdAt = 1, status = MessageStatuses.SENT,
            ).toDomain(selfId = "self").isOutgoing,
        )
        assertEquals("u2", theirs.senderId)
    }

    // ------------------------------------------------------------------ chat kind

    @Test
    fun `every chat kind survives the string round trip`() {
        for (kind in listOf(ChatKind.DIRECT, ChatKind.GROUP, ChatKind.CHANNEL)) {
            assertEquals("kind $kind did not survive", kind, kindOf(kind.toWire()))
        }
    }

    @Test
    fun `chat kind parsing is case insensitive`() {
        // The column holds whatever the server sent, and a differently-cased value
        // would silently become UNKNOWN and lose the chat's admin rules.
        assertEquals(ChatKind.DIRECT, kindOf("DIRECT"))
        assertEquals(ChatKind.GROUP, kindOf("Group"))
    }

    @Test
    fun `an unrecognised chat kind is UNKNOWN, not a guess`() {
        // Guessing "group" would grant a channel's subscribers the right to post.
        assertEquals(ChatKind.UNKNOWN, kindOf("broadcast"))
        assertEquals(ChatKind.UNKNOWN, kindOf(""))
    }

    @Test
    fun `an unknown kind is sent as group rather than as an empty string`() {
        // The server rejects an empty type outright; group is the least-privileged
        // shape that still works.
        assertEquals("group", ChatKind.UNKNOWN.toWire())
    }

    // ------------------------------------------------------------- chat to domain

    @Test
    fun `a chat with no messages has no last message`() {
        // `lastMessageAt == 0` is the "never written in" state; a LastMessage built
        // from it would render the Unix epoch in the chat list.
        val chat = ChatEntity(
            chatId = "c1", type = "group", title = "Team", createdAt = 1, lastMessageAt = 0,
        ).toDomain()

        assertNull(chat.lastMessage)
    }

    @Test
    fun `a chat with a message carries its preview`() {
        val chat = ChatEntity(
            chatId = "c1", type = "group", title = "Team", createdAt = 1,
            lastMessageAt = 5_000, lastMessageText = "hello",
            lastMessageSenderId = "u2", lastMessageSeq = 7,
        ).toDomain()

        assertEquals("hello", chat.lastMessage?.text)
        assertEquals("u2", chat.lastMessage?.senderId)
        assertEquals(7L, chat.lastMessage?.seq)
    }

    @Test
    fun `a deleted last message renders as empty rather than null`() {
        // A tombstone clears the text but keeps the row; the preview should go
        // blank, not make the chat look like it has never been used.
        val chat = ChatEntity(
            chatId = "c1", type = "group", title = "Team", createdAt = 1,
            lastMessageAt = 5_000, lastMessageText = null,
        ).toDomain()

        assertEquals("", chat.lastMessage?.text)
    }

    @Test
    fun `paging state survives to the domain`() {
        // `hasMoreHistory` is what stops the list requesting a page the server has
        // already said does not exist.
        val chat = ChatEntity(
            chatId = "c1", type = "group", title = "Team", createdAt = 1,
            oldestLoadedSeq = 12, hasMoreHistory = false, myReadSeq = 9,
        ).toDomain()

        assertEquals(12L, chat.oldestLoadedSeq)
        assertFalse(chat.hasMoreHistory)
        assertEquals(9L, chat.myReadSeq)
    }

    // --------------------------------------------------------- chat list row

    private fun row(
        chat: ChatEntity,
        otherSenderId: String? = null,
        otherSenderCount: Int = 0,
        unread: Int = 0,
    ) = ChatListRow(
        chat = chat,
        unreadCount = unread,
        otherSenderId = otherSenderId,
        otherSenderCount = otherSenderCount,
    )

    private val directChat = ChatEntity(
        chatId = "c1", type = "direct", title = "", createdAt = 1,
    )

    @Test
    fun `a direct chat is named after its peer`() {
        // A 1:1 chat has no server-side title, so the row is labelled by whoever
        // is on the other side — otherwise the whole list reads as blank rows.
        val chat = row(directChat.copy(peerUserId = "u2")).toDomain { id ->
            UserSummary(userId = id, displayName = "Bob")
        }

        assertEquals("Bob", chat.title)
        assertEquals("u2", chat.peerUserId)
    }

    /*
     * The inference exists for chats learned from an incoming message before the
     * first list sync: a NEW frame carries no type, title or membership. When
     * exactly one other person has written in the chat, that person IS the
     * conversation — and the count guard is what keeps a group from being
     * mislabelled as a 1:1 with whoever happened to speak.
     */
    @Test
    fun `a peer is inferred when exactly one other person has written`() {
        val chat = row(directChat, otherSenderId = "u2", otherSenderCount = 1).toDomain { id ->
            UserSummary(userId = id, displayName = "Bob")
        }

        assertEquals("u2", chat.peerUserId)
        assertEquals("Bob", chat.title)
    }

    @Test
    fun `no peer is inferred once two people have written`() {
        // Two speakers means it is not a 1:1, whatever the row looks like.
        val chat = row(directChat, otherSenderId = "u2", otherSenderCount = 2).toDomain { id ->
            UserSummary(userId = id, displayName = "Bob")
        }

        assertNull(chat.peerUserId)
    }

    @Test
    fun `an explicit peer wins over the inference`() {
        // The server said who the peer is; a guess must not override it.
        val chat = row(
            directChat.copy(peerUserId = "server-said"),
            otherSenderId = "guessed",
            otherSenderCount = 1,
        ).toDomain { id -> UserSummary(userId = id) }

        assertEquals("server-said", chat.peerUserId)
    }

    @Test
    fun `a server-supplied title wins over the peer name`() {
        val chat = row(
            ChatEntity(chatId = "c1", type = "group", title = "Team", createdAt = 1),
        ).toDomain { id -> UserSummary(userId = id, displayName = "Bob") }

        assertEquals("Team", chat.title)
    }

    @Test
    fun `an unresolvable peer leaves the title empty rather than crashing`() {
        // The directory has not been filled in yet — a fresh install rendering its
        // first chat list is exactly this case.
        val chat = row(directChat.copy(peerUserId = "u2")).toDomain { null }

        assertEquals("", chat.title)
        assertEquals("u2", chat.peerUserId)
    }

    @Test
    fun `the peer avatar is carried through`() {
        val chat = row(directChat.copy(peerUserId = "u2")).toDomain { id ->
            UserSummary(userId = id, displayName = "Bob", avatarRef = "avatar-1")
        }

        assertEquals("avatar-1", chat.peerAvatarRef)
    }

    @Test
    fun `the unread count is carried through`() {
        val chat = row(directChat, unread = 4).toDomain { null }
        assertEquals(4, chat.unreadCount)
    }

    // ---------------------------------------------------------------- user

    @Test
    fun `a wire profile becomes a user summary`() {
        val user = WireProfile(
            userId = "u1", username = "alice", displayName = "Alice", avatarRef = "a1",
        ).toDomain()

        assertEquals("u1", user.userId)
        assertEquals("alice", user.username)
        assertEquals("Alice", user.displayName)
        assertEquals("a1", user.avatarRef)
    }

    @Test
    fun `absent profile fields become null, not empty strings`() {
        // `displayLabel` falls through null but not through ""; an empty string
        // would win the fallback chain and render a blank name.
        val user = WireProfile(userId = "u1", username = "", displayName = "", avatarRef = "").toDomain()

        assertNull(user.username)
        assertNull(user.displayName)
        assertNull(user.avatarRef)
    }

    @Test
    fun `a user entity becomes a summary with its flags`() {
        val user = UserEntity(
            userId = "u1", username = "alice", name = "Al", displayName = "Alice",
            avatarRef = "a1", isContact = true, blocked = true,
        ).toDomain()

        assertTrue(user.isContact)
        assertTrue(user.blocked)
        assertEquals("Al", user.name)
    }

    /*
     * The label order is deliberate: our own address-book name beats the profile
     * name, because an address book exists precisely so a person can be filed
     * under the name we know them by. The short id is a last resort so a sender is
     * never rendered as an empty string.
     */
    @Test
    fun `the display label prefers our own name for someone`() {
        val user = UserSummary(userId = "u1", username = "alice", name = "Al", displayName = "Alice")
        assertEquals("Al", user.displayLabel)
    }

    @Test
    fun `the display label falls back to the profile name`() {
        val user = UserSummary(userId = "u1", username = "alice", displayName = "Alice")
        assertEquals("Alice", user.displayLabel)
    }

    @Test
    fun `the display label falls back to the handle`() {
        val user = UserSummary(userId = "u1", username = "alice")
        assertEquals("@alice", user.displayLabel)
    }

    @Test
    fun `the display label falls back to the id`() {
        assertEquals("u1", UserSummary(userId = "u1").displayLabel)
    }

    @Test
    fun `a blank name does not win the label fallback`() {
        // A name stored as whitespace would otherwise render an invisible label.
        val user = UserSummary(userId = "u1", name = "   ", displayName = "Alice")
        assertEquals("Alice", user.displayLabel)
    }
}
