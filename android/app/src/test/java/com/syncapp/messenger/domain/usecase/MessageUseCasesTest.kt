package com.syncapp.messenger.domain.usecase

import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.Chat
import com.syncapp.messenger.domain.model.ChatKind
import com.syncapp.messenger.domain.model.ChatTarget
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SendMessageUseCaseTest {

    private val target = ChatTarget.Existing("c1")

    @Test
    fun `sends the text`() = runTest {
        val messages = FakeMessageRepository()

        SendMessageUseCase(messages)(target, "hello")

        assertEquals("hello", messages.lastSendText)
        assertEquals(target, messages.lastSendTarget)
    }

    @Test
    fun `carries a reply target`() = runTest {
        val messages = FakeMessageRepository()

        SendMessageUseCase(messages)(target, "hello", replyTo = "m7")

        assertEquals("m7", messages.lastSendReplyTo)
    }

    @Test
    fun `sends no reply target for a top-level message`() = runTest {
        val messages = FakeMessageRepository()

        SendMessageUseCase(messages)(target, "hello")

        assertNull(messages.lastSendReplyTo)
    }

    /**
     * The peer's typing indicator is driven by an expiry, not by a reliable stop
     * frame. Without an explicit stop the peer sees "typing…" for several more
     * seconds *next to the message that just arrived* — which reads as a second
     * message about to appear that never does.
     */
    @Test
    fun `stops the typing indicator after sending`() = runTest {
        val messages = FakeMessageRepository()

        SendMessageUseCase(messages)(target, "hello")

        assertEquals(false, messages.lastTypingActive)
        assertEquals(listOf("sendText(c1)", "sendTyping(c1, false)"), messages.calls)
    }

    @Test
    fun `stops typing even when the send failed`() = runTest {
        // The user stopped typing either way; leaving the indicator up would
        // advertise a message that is sitting in the outbox.
        val messages = FakeMessageRepository().apply { sendTextResult = failure(AppError.Offline) }

        SendMessageUseCase(messages)(target, "hello")

        assertEquals(false, messages.lastTypingActive)
    }

    @Test
    fun `returns the send result, not the typing result`() = runTest {
        val messages = FakeMessageRepository().apply { sendTextResult = failure(AppError.Offline) }

        val result = SendMessageUseCase(messages)(target, "hello")

        assertEquals(Outcome.Failure(AppError.Offline), result)
    }

    @Test
    fun `addresses an unresolved direct peer by handle`() = runTest {
        // A direct chat has no id until its first message, so the very first send
        // has to travel addressed to "@name".
        val messages = FakeMessageRepository()

        SendMessageUseCase(messages)(ChatTarget.DirectPeer("Bob"), "hello")

        assertEquals("@bob", messages.lastSendTarget?.ref)
    }

    @Test
    fun `stops typing against the same ref it sent to`() = runTest {
        // A mismatch would leave the indicator running in the chat it was set in.
        val messages = FakeMessageRepository()

        SendMessageUseCase(messages)(ChatTarget.DirectPeer("bob"), "hello")

        assertEquals(listOf("sendText(@bob)", "sendTyping(@bob, false)"), messages.calls)
    }
}

class LoadOlderMessagesUseCaseTest {

    private fun chat(hasMore: Boolean) =
        Chat(id = "c1", kind = ChatKind.GROUP, title = "Team", hasMoreHistory = hasMore)

    @Test
    fun `loads a page when there is more history`() = runTest {
        val messages = FakeMessageRepository()

        val result = LoadOlderMessagesUseCase(messages)(chat(hasMore = true))

        assertEquals(Outcome.Success(true), result)
        assertEquals("c1", messages.lastLoadOlderChatId)
    }

    /**
     * The guard is the whole point. Reaching the top of a fully-loaded chat is
     * not a one-off event — the list fires it on every scroll frame — so without
     * it the client would hammer the gateway with HISTORY requests that can only
     * ever answer "nothing".
     */
    @Test
    fun `does not request anything once the server said there is no more`() = runTest {
        val messages = FakeMessageRepository()

        val result = LoadOlderMessagesUseCase(messages)(chat(hasMore = false))

        assertEquals(Outcome.Success(false), result)
        assertTrue("should not have hit the network", messages.calls.isEmpty())
    }

    @Test
    fun `uses the default page size`() = runTest {
        val messages = FakeMessageRepository()

        LoadOlderMessagesUseCase(messages)(chat(hasMore = true))

        assertEquals(50, messages.lastLoadOlderPageSize)
    }

    @Test
    fun `honours an explicit page size`() = runTest {
        val messages = FakeMessageRepository()

        LoadOlderMessagesUseCase(messages)(chat(hasMore = true), pageSize = 20)

        assertEquals(20, messages.lastLoadOlderPageSize)
    }

    @Test
    fun `reports false when that page turned out to be the last`() = runTest {
        // The repository's false is what flips hasMoreHistory off for next time.
        val messages = FakeMessageRepository().apply { loadOlderResult = Outcome.Success(false) }

        val result = LoadOlderMessagesUseCase(messages)(chat(hasMore = true))

        assertEquals(Outcome.Success(false), result)
    }

    @Test
    fun `propagates a failure`() = runTest {
        val messages = FakeMessageRepository().apply { loadOlderResult = failure(AppError.Timeout) }

        val result = LoadOlderMessagesUseCase(messages)(chat(hasMore = true))

        assertEquals(Outcome.Failure(AppError.Timeout), result)
    }
}

class MarkChatReadUseCaseTest {

    @Test
    fun `passes the cursor through`() = runTest {
        val messages = FakeMessageRepository()

        MarkChatReadUseCase(messages)("c1", 42)

        assertEquals(42L, messages.lastMarkReadSeq)
        assertEquals(listOf("markRead(c1, 42)"), messages.calls)
    }

    @Test
    fun `passes a zero cursor through rather than dropping it`() = runTest {
        // Deciding whether a cursor is worth sending belongs to the repository,
        // which is the layer that knows the current one.
        val messages = FakeMessageRepository()

        MarkChatReadUseCase(messages)("c1", 0)

        assertEquals(0L, messages.lastMarkReadSeq)
    }
}
