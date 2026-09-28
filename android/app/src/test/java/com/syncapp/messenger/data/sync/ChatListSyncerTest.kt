package com.syncapp.messenger.data.sync

import com.syncapp.messenger.network.protocol.ChatList
import com.syncapp.messenger.network.protocol.ChatSummary
import com.syncapp.messenger.network.protocol.Chats
import com.syncapp.messenger.network.protocol.HistoryOk
import com.syncapp.messenger.network.protocol.MsgType
import com.syncapp.messenger.network.protocol.NewMessage
import com.syncapp.messenger.network.protocol.Profile
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
 * Pulling the authoritative chat list and making the local cache agree with it.
 *
 * This is the one message that enumerates a user's chats — everything else in the
 * client learns about a chat as a consequence of traffic, which is useless for a
 * fresh install. So the failure it guards against is specific: a browser or phone
 * that has never seen any traffic showing an empty list for conversations the
 * account is already in.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ChatListSyncerTest {

    private lateinit var fixture: IngestFixture
    private lateinit var gateway: FakeGateway
    private lateinit var syncer: ChatListSyncer

    @Before
    fun setUp() {
        fixture = IngestFixture.create(selfUserId = "self")
        gateway = FakeGateway()
        val history = HistoryFetcher(gateway, fixture.db, fixture.ingestor)
        val profiles = ProfileFetcher(gateway, fixture.db)
        syncer = ChatListSyncer(gateway, fixture.db, history, profiles)
    }

    @After
    fun tearDown() = fixture.close()

    private fun summary(
        chatId: String = "c1",
        type: String = "group",
        title: String = "Team",
        peerId: String = "",
        ownerId: String = "",
        username: String = "",
        lastSeq: Long = 0,
    ) = ChatSummary(
        chatId = chatId,
        type = type,
        title = title,
        peerId = peerId,
        ownerId = ownerId,
        username = username,
        lastSeq = lastSeq,
    )

    /** One page of chats, then whatever the backfill and profile passes need. */
    private fun queuePage(
        chats: List<ChatSummary>,
        done: Boolean = true,
        nextAfter: String = "",
    ) {
        gateway.queueReply(Chats(chats = chats, done = done, nextAfter = nextAfter))
    }

    private fun chatListRequests() =
        gateway.calls.filter { it.type == MsgType.CHAT_LIST }

    // ------------------------------------------------------------ enumeration

    @Test
    fun `the list is requested from the beginning`() = runTest {
        queuePage(emptyList())

        syncer.sync()

        val body = chatListRequests().single().body as ChatList
        assertEquals("", body.after)
    }

    @Test
    fun `an enumerated chat is recorded`() = runTest {
        queuePage(listOf(summary(chatId = "c1", type = "group", title = "Team", ownerId = "u9")))

        syncer.sync()

        val chat = fixture.chats.findById("c1")
        assertNotNull(chat)
        assertEquals("group", chat!!.type)
        assertEquals("Team", chat.title)
        assertEquals("u9", chat.ownerId)
    }

    @Test
    fun `a direct chat records its peer`() = runTest {
        queuePage(listOf(summary(chatId = "c1", type = "direct", title = "", peerId = "u2")))

        syncer.sync()

        assertEquals("u2", fixture.chats.findById("c1")!!.peerUserId)
    }

    @Test
    fun `a peer is recorded as a person before their profile arrives`() = runTest {
        // The row has to exist for the profile pass to fill in, and meanwhile the
        // chat can at least be labelled by id rather than by nothing.
        queuePage(listOf(summary(chatId = "c1", type = "direct", title = "", peerId = "u2")))

        syncer.sync()

        assertNotNull(fixture.users.findById("u2"))
    }

    @Test
    fun `a row with no chat id is skipped`() = runTest {
        // A malformed row must not create an entry keyed by the empty string.
        queuePage(listOf(summary(chatId = "")))

        syncer.sync()

        assertNull(fixture.chats.findById(""))
    }

    @Test
    fun `an empty list is handled`() = runTest {
        queuePage(emptyList())

        syncer.sync()

        assertEquals(1, chatListRequests().size)
    }

    // ----------------------------------------------------------------- paging

    @Test
    fun `paging follows the cursor`() = runTest {
        queuePage(listOf(summary(chatId = "c1")), done = false, nextAfter = "cursor-1")
        queuePage(listOf(summary(chatId = "c2")), done = true)

        syncer.sync()

        assertEquals(2, chatListRequests().size)
        assertEquals("cursor-1", (chatListRequests()[1].body as ChatList).after)
        assertNotNull(fixture.chats.findById("c2"))
    }

    @Test
    fun `paging stops when the server says the list is complete`() = runTest {
        queuePage(listOf(summary(chatId = "c1")), done = true, nextAfter = "ignored")

        syncer.sync()

        assertEquals(1, chatListRequests().size)
    }

    @Test
    fun `paging stops on an empty cursor`() = runTest {
        queuePage(listOf(summary(chatId = "c1")), done = false, nextAfter = "")

        syncer.sync()

        assertEquals(1, chatListRequests().size)
    }

    /**
     * A server that keeps handing back the same cursor would otherwise cost
     * MAX_PAGES identical round trips on every connect — bounded, but twenty
     * requests to learn nothing, and twenty charges against the user's rate limit.
     *
     * The web client has always guarded this (`nextAfter === after`); this client
     * did not, which is the asymmetry the test pins.
     */
    @Test
    fun `paging stops when the cursor stops advancing`() = runTest {
        repeat(30) {
            queuePage(listOf(summary(chatId = "c1")), done = false, nextAfter = "stuck")
        }

        syncer.sync()

        assertEquals(
            "a non-advancing cursor was followed more than once",
            2,
            chatListRequests().size,
        )
    }

    // --------------------------------------------------------------- backfill

    @Test
    fun `a chat that moved is backfilled`() = runTest {
        /*
         * `lastSeq` against the newest sequence held locally is what makes a refresh
         * cheap: one history request per CHANGED chat rather than one per chat. A
         * fresh install has no local sequence at all, which is the case this whole
         * message type exists for.
         */
        queuePage(listOf(summary(chatId = "c1", lastSeq = 5)))
        gateway.queueStream(
            items = listOf(
                NewMessage(messageId = "m5", chatId = "c1", senderId = "u2", chatSeq = 5, timestamp = 1),
            ),
            end = HistoryOk(chatId = "c1", done = true),
        )

        syncer.sync()

        assertNotNull(fixture.messages.findById("m5"))
    }

    @Test
    fun `a chat that did not move is not backfilled`() = runTest {
        // The saving this design exists for: an unchanged chat costs nothing.
        fixture.ingestor.ingest(
            NewMessage(messageId = "m5", chatId = "c1", senderId = "u2", chatSeq = 5, timestamp = 1),
        )
        queuePage(listOf(summary(chatId = "c1", lastSeq = 5)))

        syncer.sync()

        assertTrue(
            "an unchanged chat was backfilled",
            gateway.calls.none { it.type == MsgType.HISTORY },
        )
    }

    @Test
    fun `a failing backfill does not abort the sync`() = runTest {
        /*
         * Best-effort by design: a chat list that renders with one conversation
         * missing its newest message is far better than one that refuses to load
         * because a single history request failed.
         */
        queuePage(listOf(summary(chatId = "c1", lastSeq = 5)))
        gateway.failType(MsgType.HISTORY, IllegalStateException("history unavailable"))

        syncer.sync()

        // The chat itself still landed, which is the part that matters.
        assertNotNull(fixture.chats.findById("c1"))
    }

    // ---------------------------------------------------------------- merging

    @Test
    fun `an existing chat keeps the data the server does not send`() = runTest {
        // A chat-list page carries no message preview and no read cursor; a replace
        // rather than a merge would blank the list and resurrect cleared badges.
        fixture.ingestor.ingest(
            NewMessage(
                messageId = "m9", chatId = "c1", senderId = "u2", chatSeq = 9,
                text = "local preview", timestamp = 1_000,
            ),
        )
        fixture.chats.advanceReadCursor("c1", 9)

        queuePage(listOf(summary(chatId = "c1", lastSeq = 9)))
        syncer.sync()

        val chat = fixture.chats.findById("c1")!!
        assertEquals("local preview", chat.lastMessageText)
        assertEquals(9L, chat.myReadSeq)
    }

    @Test
    fun `a chat learned from a live message gains its type and title`() = runTest {
        /*
         * A NEW frame names a chat id and nothing else, so such a chat sits as
         * UNKNOWN until the list sync arrives. This is the moment it becomes a
         * named group rather than an unlabelled row.
         */
        fixture.ingestor.ingest(
            NewMessage(messageId = "m1", chatId = "c1", senderId = "u2", chatSeq = 1, timestamp = 1),
        )
        assertEquals(MessageIngestor.TYPE_UNKNOWN, fixture.chats.findById("c1")!!.type)

        queuePage(listOf(summary(chatId = "c1", type = "group", title = "Team", lastSeq = 1)))
        syncer.sync()

        val chat = fixture.chats.findById("c1")!!
        assertEquals("group", chat.type)
        assertEquals("Team", chat.title)
    }

    // --------------------------------------------------------------- profiles

    @Test
    fun `profiles are fetched for people known only by id`() = runTest {
        queuePage(listOf(summary(chatId = "c1", type = "direct", title = "", peerId = "u2")))
        gateway.queueReply(
            Profile(userId = "u2", username = "bob", displayName = "Bob", avatarRef = "a1"),
        )

        syncer.sync()

        val user = fixture.users.findById("u2")
        assertEquals("Bob", user?.displayName)
        assertEquals("bob", user?.username)
    }

    @Test
    fun `a failing profile lookup does not abort the sync`() = runTest {
        // A chat list that renders a numeric id beats one that does not render.
        queuePage(listOf(summary(chatId = "c1", type = "direct", title = "", peerId = "u2")))
        gateway.failType(MsgType.PROFILE_GET, IllegalStateException("profile unavailable"))

        syncer.sync()

        assertNotNull(fixture.chats.findById("c1"))
    }

    // ----------------------------------------------------------------- errors

    @Test
    fun `a failing chat-list request propagates so the caller can classify it`() = runTest {
        // Unlike the backfill and the profile pass, this one is the whole point of
        // the call — swallowing it would report a successful sync that synced
        // nothing.
        gateway.rejectWith(code = 2000, message = "unauthenticated")

        var thrown: Throwable? = null
        try {
            syncer.sync()
        } catch (e: Throwable) {
            thrown = e
        }

        assertNotNull("the failure was swallowed", thrown)
    }
}
