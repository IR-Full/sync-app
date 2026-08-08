package com.synapse.messenger.data.mapper

import com.synapse.messenger.database.dao.ChatListRow
import com.synapse.messenger.database.entity.ChatEntity
import com.synapse.messenger.database.entity.MessageStatuses
import com.synapse.messenger.domain.model.AttachmentKind
import com.synapse.messenger.domain.model.ChatKind
import com.synapse.messenger.domain.model.MessageStatus
import com.synapse.messenger.network.protocol.Attachment
import com.synapse.messenger.network.protocol.NewMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class MapperTest {

    @Test
    fun `wire message becomes a storable row`() {
        val entity = NewMessage(
            messageId = "1",
            chatId = "2",
            senderId = "3",
            chatSeq = 5,
            text = "hi",
            timestamp = 1_000,
            attachment = Attachment(kind = "voice", mediaRef = "m1", durationMs = 3_000),
        ).toEntity()

        assertEquals("1", entity.messageId)
        assertEquals(5, entity.seq)
        assertEquals(MessageStatuses.SENT, entity.status)
        // Empty optionals become null rather than "": a column of empty strings would
        // make every "is there a reply" check a string comparison.
        assertNull(entity.replyTo)
        assertNull(entity.mediaRef)
        assertTrue(entity.attachmentJson!!.contains("voice"))
    }

    @Test
    fun `own messages are outgoing and statuses map through`() {
        val row = NewMessage(messageId = "1", chatId = "2", senderId = "me", chatSeq = 1)
            .toEntity(MessageStatuses.PENDING)

        val mine = row.toDomain(selfId = "me")
        assertTrue(mine.isOutgoing)
        assertEquals(MessageStatus.PENDING, mine.status)

        val theirs = row.toDomain(selfId = "someone-else")
        assertFalse(theirs.isOutgoing)
    }

    @Test
    fun `attachment kind survives the round trip`() {
        val stored = Attachment(kind = "video_note", mediaRef = "m2", width = 240, height = 240).toStored()
        val domain = stored.toDomain()

        assertEquals(AttachmentKind.VIDEO_NOTE, domain.kind)
        assertEquals("video_note", domain.toWire().kind)
    }

    @Test
    fun `an unknown kind falls back to file rather than failing`() {
        // Kinds are strings on the wire and the server may grow new ones; an
        // unrenderable attachment should still render as something.
        assertEquals(AttachmentKind.FILE, AttachmentKind.fromWire("hologram"))
    }

    @Test
    fun `a chat known only from incoming messages is labelled by its single writer`() {
        // NEW frames carry a chat id and nothing else — no type, no title, no
        // membership — so a one-writer chat is shown as a conversation with that person.
        val row = ChatListRow(
            chat = ChatEntity(chatId = "77", type = "unknown", createdAt = 1),
            unreadCount = 2,
            otherSenderId = "u1",
            otherSenderCount = 1,
        )

        val chat = row.toDomain { userId -> if (userId == "u1") "@alice" else null }

        assertEquals("@alice", chat.title)
        assertEquals("u1", chat.peerUserId)
        assertEquals(2, chat.unreadCount)
    }

    @Test
    fun `a chat with several writers is not attributed to one of them`() {
        val row = ChatListRow(
            chat = ChatEntity(chatId = "78", type = "unknown"),
            unreadCount = 0,
            otherSenderId = "u1",
            otherSenderCount = 3,
        )

        val chat = row.toDomain { "@alice" }

        assertEquals("", chat.title)
        assertNull(chat.peerUserId)
    }

    @Test
    fun `a declared title always wins over inference`() {
        val row = ChatListRow(
            chat = ChatEntity(chatId = "79", type = "group", title = "Release crew"),
            unreadCount = 0,
            otherSenderId = "u1",
            otherSenderCount = 1,
        )

        val chat = row.toDomain { "@alice" }

        assertEquals("Release crew", chat.title)
        assertEquals(ChatKind.GROUP, chat.kind)
    }
}
