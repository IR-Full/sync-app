package com.syncapp.messenger.data.sync

import com.syncapp.messenger.database.entity.MessageEntity
import com.syncapp.messenger.database.entity.MessageStatuses
import com.syncapp.messenger.database.entity.OutboxEntity
import com.syncapp.messenger.network.protocol.NewMessage
import com.syncapp.messenger.network.protocol.ReadUpdate
import com.syncapp.messenger.network.protocol.SendAck
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The single place a server fact becomes a local row.
 *
 * Live fanout and history backfill deliver the *same* NEW frame shape, so they
 * share this writer — which means every case here has to be idempotent. A message
 * legitimately arrives twice (fanout now, backfill later), and the gateway fans a
 * message back to its own sender, so this device sees its own sends echoed.
 *
 * Robolectric with an in-memory Room: the logic under test is mostly SQL, and a
 * fake DAO would prove nothing about the query that actually runs.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class MessageIngestorTest {

    private lateinit var fixture: IngestFixture

    @Before
    fun setUp() {
        fixture = IngestFixture.create(selfUserId = "self")
    }

    @After
    fun tearDown() {
        fixture.close()
    }

    private fun message(
        id: String = "m1",
        chatId: String = "c1",
        senderId: String = "u2",
        seq: Long = 1,
        text: String = "hello",
        timestamp: Long = 1_000,
        deleted: Boolean = false,
    ) = NewMessage(
        messageId = id,
        chatId = chatId,
        senderId = senderId,
        chatSeq = seq,
        text = text,
        timestamp = timestamp,
        deleted = deleted,
    )

    // ------------------------------------------------------------------ ingest

    @Test
    fun `an incoming message becomes a row`() = runTest {
        fixture.ingestor.ingest(message())

        val stored = fixture.messages.findById("m1")
        assertNotNull(stored)
        assertEquals("hello", stored!!.text)
        assertEquals(1L, stored.seq)
    }

    @Test
    fun `a message creates the chat it names`() = runTest {
        // A NEW frame names a chat id and nothing else — no type, no title, no
        // membership — so the chat has to be recorded as UNKNOWN and labelled
        // later from whoever writes in it.
        fixture.ingestor.ingest(message())

        val chat = fixture.chats.findById("c1")
        assertNotNull(chat)
        assertEquals(MessageIngestor.TYPE_UNKNOWN, chat!!.type)
    }

    @Test
    fun `ingesting the same message twice leaves one row`() = runTest {
        // Fanout now, backfill later — the same message arrives twice by design.
        fixture.ingestor.ingest(message())
        fixture.ingestor.ingest(message())

        assertEquals(1L, fixture.messages.newestSeq("c1"))
        assertNotNull(fixture.messages.findById("m1"))
    }

    @Test
    fun `an empty chat id is skipped rather than creating a phantom chat`() = runTest {
        fixture.ingestor.ingest(message(chatId = ""))

        assertNull(fixture.chats.findById(""))
        assertNull(fixture.messages.findById("m1"))
    }

    @Test
    fun `a batch is written in one pass`() = runTest {
        fixture.ingestor.ingestAll(
            listOf(
                message(id = "m1", seq = 1),
                message(id = "m2", seq = 2),
                message(id = "m3", seq = 3),
            ),
        )

        assertEquals(3L, fixture.messages.newestSeq("c1"))
    }

    @Test
    fun `an empty batch is a no-op`() = runTest {
        fixture.ingestor.ingestAll(emptyList())
        assertNull(fixture.chats.findById("c1"))
    }

    // ------------------------------------------------------------ chat preview

    @Test
    fun `an incoming message updates the chat preview`() = runTest {
        fixture.ingestor.ingest(message(text = "hello"))

        val chat = fixture.chats.findById("c1")!!
        assertEquals("hello", chat.lastMessageText)
        assertEquals("u2", chat.lastMessageSenderId)
        assertEquals(1L, chat.lastMessageSeq)
    }

    @Test
    fun `a backfill can be asked not to move the preview`() = runTest {
        /*
         * History backfill replays OLDER messages through this same path. Letting
         * them bump the preview would rewrite the chat list with a message from
         * last week every time a user scrolled up.
         */
        fixture.ingestor.ingest(message(id = "new", seq = 10, text = "latest", timestamp = 5_000))
        fixture.ingestor.ingest(
            message(id = "old", seq = 2, text = "ancient", timestamp = 1_000),
            bumpChat = false,
        )

        assertEquals("latest", fixture.chats.findById("c1")!!.lastMessageText)
    }

    @Test
    fun `a deleted message previews as empty rather than as its old text`() = runTest {
        // The tombstone keeps the row; the preview must not keep the content the
        // sender just retracted.
        fixture.ingestor.ingest(message(text = "regrettable", deleted = true))

        assertEquals("", fixture.chats.findById("c1")!!.lastMessageText)
    }

    @Test
    fun `a media message previews as an attachment marker`() = runTest {
        // A caption-less photo would otherwise show a blank row in the chat list.
        fixture.ingestor.ingest(
            NewMessage(
                messageId = "m1", chatId = "c1", senderId = "u2", chatSeq = 1,
                text = "", mediaRef = "media-1", timestamp = 1_000,
            ),
        )

        assertEquals(
            MessageIngestor.ATTACHMENT_PREVIEW,
            fixture.chats.findById("c1")!!.lastMessageText,
        )
    }

    // ------------------------------------------------------------ read cursor

    @Test
    fun `our own message advances our read cursor`() = runTest {
        /*
         * Our own sends are read by definition. Without this the unread badge would
         * count the messages we just wrote ourselves — visible immediately, and
         * confusing enough that users report it as "the badge is stuck".
         */
        fixture.ingestor.ingest(message(senderId = "self", seq = 7))

        assertEquals(7L, fixture.chats.findById("c1")!!.myReadSeq)
    }

    @Test
    fun `someone else's message does not advance our read cursor`() = runTest {
        fixture.ingestor.ingest(message(senderId = "u2", seq = 7))

        assertEquals(0L, fixture.chats.findById("c1")!!.myReadSeq)
    }

    @Test
    fun `the read cursor never moves backwards`() = runTest {
        // Backfill replays older messages of ours; a cursor that regressed would
        // resurrect an unread badge the user had already cleared.
        fixture.ingestor.ingest(message(id = "new", senderId = "self", seq = 9))
        fixture.ingestor.ingest(message(id = "old", senderId = "self", seq = 3))

        assertEquals(9L, fixture.chats.findById("c1")!!.myReadSeq)
    }

    // --------------------------------------------------------------- status

    @Test
    fun `a backfill does not downgrade a message already known to be read`() = runTest {
        /*
         * The status column drives the tick. A re-delivered message that reset it to
         * "sent" would un-tick a message the user has watched being read — a visible
         * regression with no cause the user could act on.
         */
        fixture.messages.upsert(
            MessageEntity(
                messageId = "m1", chatId = "c1", senderId = "self", seq = 1, text = "hello",
                createdAt = 1_000, status = MessageStatuses.READ,
            ),
        )

        fixture.ingestor.ingest(message(id = "m1", senderId = "self"))

        assertEquals(MessageStatuses.READ, fixture.messages.findById("m1")!!.status)
    }

    @Test
    fun `a re-delivered message keeps its dedup key`() = runTest {
        // The key links the row to its outbox entry; losing it on a redelivery
        // would strand the outbox item and replay the send forever.
        fixture.messages.upsert(
            MessageEntity(
                messageId = "m1", chatId = "c1", senderId = "self", seq = 1, text = "hello",
                createdAt = 1_000, status = MessageStatuses.SENT, dedupKey = "key-1",
            ),
        )

        fixture.ingestor.ingest(message(id = "m1", senderId = "self"))

        assertEquals("key-1", fixture.messages.findById("m1")!!.dedupKey)
    }

    // ------------------------------------------------------------- ingestAck

    @Test
    fun `an ack replaces the local row with the confirmed one`() = runTest {
        /*
         * One transaction: the optimistic row disappears exactly as the confirmed
         * one appears. Two statements would leave a window in which the transcript
         * shows the message twice — brief, but a user scrolling at that moment sees
         * it.
         */
        fixture.messages.upsert(
            MessageEntity(
                messageId = "local-1", chatId = "c1", senderId = "self", seq = 0,
                text = "hello", createdAt = 1_000,
                status = MessageStatuses.PENDING, dedupKey = "key-1",
            ),
        )

        fixture.ingestor.ingestAck(
            SendAck(dedupKey = "key-1", messageId = "server-1", chatId = "c1", chatSeq = 7, timestamp = 2_000),
            localId = "local-1",
            targetRef = "c1",
            peerUsername = null,
        )

        assertNull("the optimistic row survived", fixture.messages.findById("local-1"))
        val confirmed = fixture.messages.findById("server-1")
        assertNotNull(confirmed)
        assertEquals(7L, confirmed!!.seq)
        assertEquals(MessageStatuses.SENT, confirmed.status)
    }

    @Test
    fun `an ack keeps the text the local row held`() = runTest {
        // SEND_ACK carries ids and a sequence, not the message body — the text has
        // to come from the row being replaced or the message arrives empty.
        fixture.messages.upsert(
            MessageEntity(
                messageId = "local-1", chatId = "c1", senderId = "self", seq = 0,
                text = "the actual message", createdAt = 1_000,
                status = MessageStatuses.PENDING, dedupKey = "key-1",
            ),
        )

        fixture.ingestor.ingestAck(
            SendAck(dedupKey = "key-1", messageId = "server-1", chatId = "c1", chatSeq = 7, timestamp = 2_000),
            localId = "local-1",
            targetRef = "c1",
            peerUsername = null,
        )

        assertEquals("the actual message", fixture.messages.findById("server-1")!!.text)
    }

    @Test
    fun `an ack clears the outbox entry`() = runTest {
        // Left behind, it would be replayed on the next reconnect — harmless thanks
        // to the dedup key, but it also keeps the pending bubble on screen.
        fixture.outbox.enqueue(
            OutboxEntity(dedupKey = "key-1", targetRef = "c1", text = "hello", createdAt = 1_000),
        )

        fixture.ingestor.ingestAck(
            SendAck(dedupKey = "key-1", messageId = "server-1", chatId = "c1", chatSeq = 7, timestamp = 2_000),
            localId = "local-1",
            targetRef = "c1",
            peerUsername = null,
        )

        assertTrue(fixture.outbox.pending().none { it.dedupKey == "key-1" })
    }

    @Test
    fun `an ack advances our own read cursor`() = runTest {
        fixture.ingestor.ingestAck(
            SendAck(dedupKey = "key-1", messageId = "server-1", chatId = "c1", chatSeq = 7, timestamp = 2_000),
            localId = "local-1",
            targetRef = "c1",
            peerUsername = null,
        )

        assertEquals(7L, fixture.chats.findById("c1")!!.myReadSeq)
    }

    @Test
    fun `an ack updates the chat preview`() = runTest {
        fixture.messages.upsert(
            MessageEntity(
                messageId = "local-1", chatId = "c1", senderId = "self", seq = 0,
                text = "hello", createdAt = 1_000,
                status = MessageStatuses.PENDING, dedupKey = "key-1",
            ),
        )

        fixture.ingestor.ingestAck(
            SendAck(dedupKey = "key-1", messageId = "server-1", chatId = "c1", chatSeq = 7, timestamp = 2_000),
            localId = "local-1",
            targetRef = "c1",
            peerUsername = null,
        )

        assertEquals("hello", fixture.chats.findById("c1")!!.lastMessageText)
    }

    @Test
    fun `an ack with no timestamp falls back to the local one`() = runTest {
        // An ordering stamp of 0 would file the message at the top of the
        // transcript, above everything sent since 1970.
        fixture.messages.upsert(
            MessageEntity(
                messageId = "local-1", chatId = "c1", senderId = "self", seq = 0,
                text = "hello", createdAt = 4_242,
                status = MessageStatuses.PENDING, dedupKey = "key-1",
            ),
        )

        fixture.ingestor.ingestAck(
            SendAck(dedupKey = "key-1", messageId = "server-1", chatId = "c1", chatSeq = 7, timestamp = 0),
            localId = "local-1",
            targetRef = "c1",
            peerUsername = null,
        )

        assertEquals(4_242L, fixture.messages.findById("server-1")!!.createdAt)
    }

    // ------------------------------------------------------ placeholder promotion

    /*
     * A direct chat does not exist server-side until the first message resolves it,
     * so a conversation can be opened — and composed offline — before it has an id.
     * Everything is filed under a "@username" placeholder until the ack names the
     * real chat. If the promotion missed anything, that content would simply
     * disappear from the user's view while still being in the database.
     */

    @Test
    fun `an ack for a handle target promotes the placeholder`() = runTest {
        fixture.chats.upsertKnown(chatId = "@bob", type = MessageIngestor.TYPE_DIRECT, createdAt = 1_000)

        fixture.ingestor.ingestAck(
            SendAck(dedupKey = "key-1", messageId = "server-1", chatId = "real-chat", chatSeq = 1, timestamp = 2_000),
            localId = "local-1",
            targetRef = "@bob",
            peerUsername = "bob",
        )

        assertNull("the placeholder survived", fixture.chats.findById("@bob"))
        assertNotNull(fixture.chats.findById("real-chat"))
    }

    @Test
    fun `promotion moves the messages across`() = runTest {
        fixture.chats.upsertKnown(chatId = "@bob", type = MessageIngestor.TYPE_DIRECT, createdAt = 1_000)
        fixture.messages.upsert(
            MessageEntity(
                messageId = "m1", chatId = "@bob", senderId = "self", seq = 1,
                text = "earlier", createdAt = 1_000, status = MessageStatuses.SENT,
            ),
        )

        fixture.ingestor.promotePlaceholder("@bob", "real-chat", "bob")

        assertEquals("real-chat", fixture.messages.findById("m1")!!.chatId)
    }

    @Test
    fun `promotion retargets queued messages`() = runTest {
        // Composed offline against a handle; the queue has to follow the chat or
        // the flush would send them to a chat ref that no longer exists.
        fixture.chats.upsertKnown(chatId = "@bob", type = MessageIngestor.TYPE_DIRECT, createdAt = 1_000)
        fixture.outbox.enqueue(
            OutboxEntity(dedupKey = "key-1", targetRef = "@bob", text = "queued", createdAt = 1_000),
        )

        fixture.ingestor.promotePlaceholder("@bob", "real-chat", "bob")

        assertEquals("real-chat", fixture.outbox.pending().single().targetRef)
    }

    @Test
    fun `promotion records the peer handle on the real chat`() = runTest {
        // It is what lets "message @bob" later find the conversation that already
        // exists instead of opening a second one.
        fixture.chats.upsertKnown(chatId = "@bob", type = MessageIngestor.TYPE_DIRECT, createdAt = 1_000)

        fixture.ingestor.promotePlaceholder("@bob", "real-chat", "bob")

        assertEquals("bob", fixture.chats.findById("real-chat")!!.peerUsername)
    }

    @Test
    fun `promotion marks the real chat as direct`() = runTest {
        fixture.chats.upsertKnown(chatId = "@bob", type = MessageIngestor.TYPE_DIRECT, createdAt = 1_000)

        fixture.ingestor.promotePlaceholder("@bob", "real-chat", "bob")

        assertEquals(MessageIngestor.TYPE_DIRECT, fixture.chats.findById("real-chat")!!.type)
    }

    @Test
    fun `promoting onto an existing chat keeps the existing one`() = runTest {
        // The real chat may already be known from a CHAT_LIST sync; overwriting it
        // would discard the title, owner and read cursor that sync supplied.
        fixture.chats.upsertKnown(chatId = "@bob", type = MessageIngestor.TYPE_DIRECT, createdAt = 1_000)
        fixture.chats.upsertKnown(chatId = "real-chat", type = MessageIngestor.TYPE_DIRECT, createdAt = 500)
        fixture.chats.advanceReadCursor("real-chat", 12)

        fixture.ingestor.promotePlaceholder("@bob", "real-chat", "bob")

        assertEquals(12L, fixture.chats.findById("real-chat")!!.myReadSeq)
    }

    @Test
    fun `promoting a chat onto itself is a no-op`() = runTest {
        // Guards the ordinary case, where the send was addressed to a real chat id
        // and no promotion is needed at all.
        fixture.chats.upsertKnown(chatId = "c1", type = MessageIngestor.TYPE_DIRECT, createdAt = 1_000)

        fixture.ingestor.promotePlaceholder("c1", "c1", null)

        assertNotNull("the chat deleted itself", fixture.chats.findById("c1"))
    }

    // ----------------------------------------------------------- read receipts

    @Test
    fun `another member's read cursor is stored`() = runTest {
        fixture.ingestor.ingestReceipt(ReadUpdate(chatId = "c1", userId = "u2", upToChatSeq = 5))

        assertEquals(5L, fixture.receipts.observeOthersReadSeq("c1", "self").first())
    }

    @Test
    fun `our own cursor moved on another device advances the local one`() = runTest {
        // Reading on a phone has to clear the badge on a laptop.
        fixture.chats.upsertKnown(chatId = "c1", type = MessageIngestor.TYPE_UNKNOWN, createdAt = 1)

        fixture.ingestor.ingestReceipt(ReadUpdate(chatId = "c1", userId = "self", upToChatSeq = 9))

        assertEquals(9L, fixture.chats.findById("c1")!!.myReadSeq)
    }

    @Test
    fun `a receipt with no chat or user is ignored`() = runTest {
        fixture.ingestor.ingestReceipt(ReadUpdate(chatId = "", userId = "u2", upToChatSeq = 5))
        fixture.ingestor.ingestReceipt(ReadUpdate(chatId = "c1", userId = "", upToChatSeq = 5))

        assertEquals(0L, fixture.receipts.observeOthersReadSeq("c1", "self").first())
    }

    // ------------------------------------------------------- delivery receipts

    @Test
    fun `a delivery receipt is stored`() = runTest {
        fixture.ingestor.ingestDelivery(ReadUpdate(chatId = "c1", userId = "u2", upToChatSeq = 5))

        assertEquals(5L, fixture.receipts.observeOthersDeliveredSeq("c1", "self").first())
    }

    @Test
    fun `a delivery receipt about ourselves is never even stored`() = runTest {
        /*
         * Our own other device receiving a message says nothing about whether the
         * OTHER party did. Counting it would show a delivered tick the moment a
         * second device of ours came online — on a message the recipient has not
         * seen at all.
         *
         * Asserted by reading the table from a DIFFERENT user's point of view. The
         * obvious query, `observeOthersDeliveredSeq(chat, "self")`, already excludes
         * `userId = "self"` in its own WHERE clause, so it reports 0 whether or not
         * the row was written — a test built on it passes with the guard deleted,
         * which is exactly what happened to the first version of this one.
         */
        fixture.ingestor.ingestDelivery(ReadUpdate(chatId = "c1", userId = "self", upToChatSeq = 9))

        val visibleToSomeoneElse =
            fixture.receipts.observeOthersDeliveredSeq("c1", "a-different-user").first()
        assertEquals(
            "a receipt about our own device reached the delivery table",
            0L,
            visibleToSomeoneElse,
        )
    }

    @Test
    fun `a delivery receipt about a peer is stored and visible`() = runTest {
        // The positive half, read the same way, so the assertion above is known to
        // be capable of seeing a row at all.
        fixture.ingestor.ingestDelivery(ReadUpdate(chatId = "c1", userId = "u2", upToChatSeq = 9))

        assertEquals(9L, fixture.receipts.observeOthersDeliveredSeq("c1", "a-different-user").first())
    }

    @Test
    fun `a delivery receipt with no chat or user is ignored`() = runTest {
        fixture.ingestor.ingestDelivery(ReadUpdate(chatId = "", userId = "u2", upToChatSeq = 5))
        fixture.ingestor.ingestDelivery(ReadUpdate(chatId = "c1", userId = "", upToChatSeq = 5))

        assertEquals(0L, fixture.receipts.observeOthersDeliveredSeq("c1", "self").first())
    }

    // ------------------------------------------------------------ history floor

    @Test
    fun `ingesting an older message lowers the history floor`() = runTest {
        // The floor is where "load older" starts from; leaving it high would make
        // the client re-request pages it already holds.
        fixture.ingestor.ingest(message(id = "m5", seq = 5))
        fixture.ingestor.ingest(message(id = "m2", seq = 2))

        assertEquals(2L, fixture.chats.findById("c1")!!.oldestLoadedSeq)
    }

    @Test
    fun `a newer message does not raise the history floor`() = runTest {
        // Raising it would make the client think it holds less than it does and
        // re-fetch the gap on every scroll to the top.
        fixture.ingestor.ingest(message(id = "m2", seq = 2))
        fixture.ingestor.ingest(message(id = "m9", seq = 9))

        assertEquals(2L, fixture.chats.findById("c1")!!.oldestLoadedSeq)
    }
}
