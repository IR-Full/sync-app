package com.syncapp.messenger.data.sync

import com.syncapp.messenger.network.protocol.HistoryOk
import com.syncapp.messenger.network.protocol.MsgType
import com.syncapp.messenger.network.protocol.NewMessage
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Paging over HISTORY.
 *
 * Two protocol details make this worth testing on its own: the reply is a
 * *stream* (N NEW frames plus a HISTORY_OK terminator, not a list), and history
 * walks BACKWARDS from a cursor while the UI reads forwards. Both are easy to get
 * subtly wrong in a way that looks like "the chat is missing some messages".
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class HistoryFetcherTest {

    private lateinit var fixture: IngestFixture
    private lateinit var gateway: FakeGateway
    private lateinit var fetcher: HistoryFetcher

    @Before
    fun setUp() {
        fixture = IngestFixture.create(selfUserId = "self")
        gateway = FakeGateway()
        fetcher = HistoryFetcher(gateway, fixture.db, fixture.ingestor)
    }

    @After
    fun tearDown() = fixture.close()

    private fun message(id: String, seq: Long, chatId: String = "c1") = NewMessage(
        messageId = id,
        chatId = chatId,
        senderId = "u2",
        chatSeq = seq,
        text = "m$seq",
        timestamp = 1_000 + seq,
    )

    // ------------------------------------------------------------------- page

    @Test
    fun `a page is requested as a stream of NEW frames`() = runTest {
        // The terminator type is implied; the item type is what the client has to
        // name, and naming the wrong one would collect nothing.
        gateway.queueStream(items = emptyList(), end = HistoryOk(chatId = "c1", done = true))

        fetcher.page("c1", beforeSeq = 0, limit = 50)

        val call = gateway.calls.single()
        assertEquals(MsgType.HISTORY, call.type)
        assertEquals(MsgType.NEW, call.itemType)
    }

    @Test
    fun `a page carries the cursor and the limit`() = runTest {
        gateway.queueStream(items = emptyList(), end = HistoryOk(chatId = "c1", done = true))

        fetcher.page("c1", beforeSeq = 42, limit = 25)

        val body = gateway.calls.single().body as com.syncapp.messenger.network.protocol.History
        assertEquals("c1", body.chatId)
        assertEquals(42L, body.beforeSeq)
        assertEquals(25, body.limit)
    }

    @Test
    fun `a page is sorted oldest first`() = runTest {
        /*
         * The gateway replays newest-first because that is how it pages backwards.
         * The transcript reads the other way, and the ingest path derives the
         * history floor from the oldest row — so leaving them reversed would put
         * the floor at the wrong end.
         */
        gateway.queueStream(
            items = listOf(message("m3", 3), message("m1", 1), message("m2", 2)),
            end = HistoryOk(chatId = "c1", done = true),
        )

        val page = fetcher.page("c1", beforeSeq = 0, limit = 50)

        assertEquals(listOf(1L, 2L, 3L), page.messages.map { it.chatSeq })
    }

    @Test
    fun `a page reports the cursor for the next one`() = runTest {
        gateway.queueStream(
            items = listOf(message("m5", 5)),
            end = HistoryOk(chatId = "c1", done = false, nextBefore = 5),
        )

        val page = fetcher.page("c1", beforeSeq = 0, limit = 50)

        assertFalse(page.done)
        assertEquals(5L, page.nextBefore)
    }

    @Test
    fun `a reply with no terminator is treated as the end`() = runTest {
        /*
         * A missing HISTORY_OK means the stream ended without a cursor to continue
         * from. Assuming "more" would make the client loop asking for a page it can
         * never receive; assuming "done" stops cleanly and the next connect retries.
         */
        gateway.queueStream(items = listOf(message("m1", 1)), end = null)

        val page = fetcher.page("c1", beforeSeq = 0, limit = 50)

        assertTrue(page.done)
        assertEquals(0L, page.nextBefore)
    }

    @Test
    fun `non-message frames in the stream are ignored`() = runTest {
        // The stream carries whatever shares the request id; anything that is not a
        // message must not be coerced into one.
        gateway.queueStream(
            items = listOf(message("m1", 1), "unexpected", 42),
            end = HistoryOk(chatId = "c1", done = true),
        )

        val page = fetcher.page("c1", beforeSeq = 0, limit = 50)

        assertEquals(1, page.messages.size)
    }

    // ---------------------------------------------------------- refreshNewest

    @Test
    fun `refreshing the newest page asks from the top`() = runTest {
        // `beforeSeq = 0` means "from the newest"; any other value would skip the
        // messages that arrived while the app was closed.
        gateway.queueStream(items = emptyList(), end = HistoryOk(chatId = "c1", done = true))

        fetcher.refreshNewest("c1")

        val body = gateway.calls.single().body as com.syncapp.messenger.network.protocol.History
        assertEquals(0L, body.beforeSeq)
    }

    @Test
    fun `refreshing files the messages it received`() = runTest {
        gateway.queueStream(
            items = listOf(message("m1", 1), message("m2", 2)),
            end = HistoryOk(chatId = "c1", done = true),
        )

        fetcher.refreshNewest("c1")

        assertEquals(2L, fixture.messages.newestSeq("c1"))
        assertNotNull(fixture.messages.findById("m1"))
    }

    @Test
    fun `refreshing records the history floor`() = runTest {
        // The floor is where "load older" starts; leaving it at 0 would make the
        // next backfill re-request the page just fetched.
        gateway.queueStream(
            items = listOf(message("m5", 5), message("m6", 6)),
            end = HistoryOk(chatId = "c1", done = true),
        )

        fetcher.refreshNewest("c1")

        assertEquals(5L, fixture.chats.findById("c1")!!.oldestLoadedSeq)
    }

    @Test
    fun `a short newest page means there is nothing older`() = runTest {
        /*
         * `done` on the newest page is the server saying it returned everything it
         * had. Keeping `hasMoreHistory` true would leave the chat permanently
         * offering a "load older" that returns nothing.
         */
        gateway.queueStream(
            items = listOf(message("m1", 1)),
            end = HistoryOk(chatId = "c1", done = true),
        )

        fetcher.refreshNewest("c1")

        assertFalse(fixture.chats.findById("c1")!!.hasMoreHistory)
    }

    @Test
    fun `a full newest page leaves the older-history assumption alone`() = runTest {
        // A full page says nothing about what is further back — only that this one
        // was full. Flipping the flag either way would be a guess.
        gateway.queueStream(
            items = listOf(message("m9", 9)),
            end = HistoryOk(chatId = "c1", done = false, nextBefore = 9),
        )

        fetcher.refreshNewest("c1")

        assertTrue(fixture.chats.findById("c1")!!.hasMoreHistory)
    }

    @Test
    fun `an empty refresh does not touch the history cursor`() = runTest {
        // A chat with no messages yet has nothing to say about its floor; writing 0
        // would claim we hold history we do not.
        fixture.chats.upsertKnown(chatId = "c1", type = "group", createdAt = 1)
        fixture.chats.updateHistoryCursor("c1", 7, hasMore = true)
        gateway.queueStream(items = emptyList(), end = HistoryOk(chatId = "c1", done = true))

        fetcher.refreshNewest("c1")

        assertEquals(7L, fixture.chats.findById("c1")!!.oldestLoadedSeq)
    }

    @Test
    fun `a failing refresh propagates so the caller can classify it`() = runTest {
        gateway.rejectWith(code = 3000, message = "not a member")

        var thrown: Throwable? = null
        try {
            fetcher.refreshNewest("c1")
        } catch (e: Throwable) {
            thrown = e
        }

        assertNotNull("the failure was swallowed", thrown)
    }

    // -------------------------------------------------------------- loadOlder

    @Test
    fun `loading older pages from the current floor`() = runTest {
        fixture.chats.upsertKnown(chatId = "c1", type = "group", createdAt = 1)
        fixture.chats.updateHistoryCursor("c1", 10, hasMore = true)
        gateway.queueStream(items = emptyList(), end = HistoryOk(chatId = "c1", done = true))

        fetcher.loadOlder("c1")

        val body = gateway.calls.single().body as com.syncapp.messenger.network.protocol.History
        assertEquals(10L, body.beforeSeq)
    }

    @Test
    fun `loading older does not reorder the chat list`() = runTest {
        /*
         * An older page is not new activity. Bumping the chat's last message would
         * make scrolling up through a quiet conversation jump it to the top of the
         * list, which reads as "someone just wrote".
         */
        fixture.ingestor.ingest(message("m9", 9))
        val previewBefore = fixture.chats.findById("c1")!!.lastMessageText

        fixture.chats.updateHistoryCursor("c1", 9, hasMore = true)
        gateway.queueStream(
            items = listOf(message("m2", 2)),
            end = HistoryOk(chatId = "c1", done = true),
        )

        fetcher.loadOlder("c1")

        assertEquals(previewBefore, fixture.chats.findById("c1")!!.lastMessageText)
    }

    @Test
    fun `loading older files the messages and lowers the floor`() = runTest {
        fixture.ingestor.ingest(message("m9", 9))
        fixture.chats.updateHistoryCursor("c1", 9, hasMore = true)
        gateway.queueStream(
            items = listOf(message("m2", 2), message("m3", 3)),
            end = HistoryOk(chatId = "c1", done = false, nextBefore = 2),
        )

        fetcher.loadOlder("c1")

        assertNotNull(fixture.messages.findById("m2"))
        assertEquals(2L, fixture.chats.findById("c1")!!.oldestLoadedSeq)
    }

    @Test
    fun `loading older reports that more remain`() = runTest {
        fixture.chats.upsertKnown(chatId = "c1", type = "group", createdAt = 1)
        fixture.chats.updateHistoryCursor("c1", 10, hasMore = true)
        gateway.queueStream(
            items = listOf(message("m2", 2)),
            end = HistoryOk(chatId = "c1", done = false, nextBefore = 2),
        )

        assertTrue(fetcher.loadOlder("c1"))
    }

    @Test
    fun `an empty older page ends the walk`() = runTest {
        /*
         * The guard that matters: reaching the top of a fully-loaded chat is not a
         * one-off event — the list fires it on every scroll frame. Reporting "more"
         * on an empty page would make the client ask again immediately, forever.
         */
        fixture.chats.upsertKnown(chatId = "c1", type = "group", createdAt = 1)
        fixture.chats.updateHistoryCursor("c1", 10, hasMore = true)
        gateway.queueStream(items = emptyList(), end = HistoryOk(chatId = "c1", done = false))

        val more = fetcher.loadOlder("c1")

        assertFalse("an empty page reported more history", more)
        assertFalse(fixture.chats.findById("c1")!!.hasMoreHistory)
    }

    @Test
    fun `loading older from an unknown chat asks for nothing`() = runTest {
        // Nothing to page from, and a request would create noise the server has to
        // answer.
        val more = fetcher.loadOlder("never-seen")

        assertFalse(more)
        assertEquals(0, gateway.requestCount)
    }

    @Test
    fun `a chat with no recorded floor falls back to its oldest row`() = runTest {
        // A chat learned from a live message has messages but no cursor yet.
        fixture.ingestor.ingest(message("m5", 5))
        fixture.chats.updateHistoryCursor("c1", 0, hasMore = true)
        gateway.queueStream(items = emptyList(), end = HistoryOk(chatId = "c1", done = true))

        fetcher.loadOlder("c1")

        val body = gateway.calls.single().body as com.syncapp.messenger.network.protocol.History
        assertEquals(5L, body.beforeSeq)
    }
}
