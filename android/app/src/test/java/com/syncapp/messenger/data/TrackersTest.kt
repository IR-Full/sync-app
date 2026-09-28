package com.syncapp.messenger.data

import app.cash.turbine.test
import com.syncapp.messenger.data.sync.PresenceTracker
import com.syncapp.messenger.data.sync.TypingTracker
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Which chat is on screen, used by the push path to suppress a notification for
 * a message the user is watching arrive.
 */
class ActiveChatTrackerTest {

    @Test
    fun `starts with no active chat`() {
        assertEquals("", ActiveChatTracker().chatId.value)
    }

    @Test
    fun `records the chat on screen`() {
        val tracker = ActiveChatTracker()
        tracker.setActive("c1")
        assertEquals("c1", tracker.chatId.value)
    }

    @Test
    fun `replaces the previous chat on navigation`() {
        val tracker = ActiveChatTracker()
        tracker.setActive("c1")
        tracker.setActive("c2")
        assertEquals("c2", tracker.chatId.value)
    }

    @Test
    fun `clearing the active chat empties it`() {
        val tracker = ActiveChatTracker()
        tracker.setActive("c1")
        tracker.clear("c1")
        assertEquals("", tracker.chatId.value)
    }

    /**
     * Compose disposes a screen *after* the next one has appeared, so the
     * teardown of the chat the user just left runs once the new chat has already
     * registered itself. An unconditional clear would therefore wipe the chat
     * that is actually on screen — and the user would get a notification for the
     * conversation they are reading.
     */
    @Test
    fun `a late disposal does not clear the chat that just opened`() {
        val tracker = ActiveChatTracker()
        tracker.setActive("c1")
        tracker.setActive("c2") // navigated
        tracker.clear("c1") // c1's disposal arrives afterwards

        assertEquals("c2", tracker.chatId.value)
    }

    @Test
    fun `clearing a chat that was never active is harmless`() {
        val tracker = ActiveChatTracker()
        tracker.clear("c1")
        assertEquals("", tracker.chatId.value)
    }

    @Test
    fun `emits changes to observers`() = runTest {
        val tracker = ActiveChatTracker()
        tracker.chatId.test {
            assertEquals("", awaitItem())
            tracker.setActive("c1")
            assertEquals("c1", awaitItem())
            tracker.clear("c1")
            assertEquals("", awaitItem())
        }
    }
}

/**
 * Typing indicators. The gateway throttles TYPING to roughly one frame per chat
 * every two seconds and classifies it as droppable — including the frame that
 * says someone *stopped*. So each signal expires on its own rather than waiting
 * to be turned off.
 */
class TypingTrackerTest {

    @Test
    fun `reports someone typing`() = runTest {
        val tracker = TypingTracker()
        tracker.onTyping("c1", "u2", active = true)

        tracker.observe("c1").test {
            assertEquals(setOf("u2"), awaitItem())
        }
    }

    @Test
    fun `reports nobody in a chat with no signals`() = runTest {
        TypingTracker().observe("c1").test {
            assertEquals(emptySet<String>(), awaitItem())
        }
    }

    @Test
    fun `stops reporting after an explicit stop`() = runTest {
        val tracker = TypingTracker()
        tracker.onTyping("c1", "u2", active = true)
        tracker.onTyping("c1", "u2", active = false)

        tracker.observe("c1").test {
            assertEquals(emptySet<String>(), awaitItem())
        }
    }

    @Test
    fun `tracks several people in one chat`() = runTest {
        val tracker = TypingTracker()
        tracker.onTyping("c1", "u2", active = true)
        tracker.onTyping("c1", "u3", active = true)

        tracker.observe("c1").test {
            assertEquals(setOf("u2", "u3"), awaitItem())
        }
    }

    @Test
    fun `keeps chats separate`() = runTest {
        val tracker = TypingTracker()
        tracker.onTyping("c1", "u2", active = true)

        tracker.observe("c2").test {
            assertEquals(emptySet<String>(), awaitItem())
        }
    }

    @Test
    fun `stopping one person leaves the others`() = runTest {
        val tracker = TypingTracker()
        tracker.onTyping("c1", "u2", active = true)
        tracker.onTyping("c1", "u3", active = true)
        tracker.onTyping("c1", "u2", active = false)

        tracker.observe("c1").test {
            assertEquals(setOf("u3"), awaitItem())
        }
    }

    @Test
    fun `ignores a signal with no chat`() = runTest {
        // A malformed frame must not create a phantom entry keyed by "".
        val tracker = TypingTracker()
        tracker.onTyping("", "u2", active = true)

        tracker.observe("").test {
            assertEquals(emptySet<String>(), awaitItem())
        }
    }

    @Test
    fun `ignores a signal with no user`() = runTest {
        val tracker = TypingTracker()
        tracker.onTyping("c1", "", active = true)

        tracker.observe("c1").test {
            assertEquals(emptySet<String>(), awaitItem())
        }
    }

    @Test
    fun `tolerates a stop for someone who was not typing`() = runTest {
        // Frames are droppable in both directions, so a stop can arrive alone.
        val tracker = TypingTracker()
        tracker.onTyping("c1", "u2", active = false)

        tracker.observe("c1").test {
            assertEquals(emptySet<String>(), awaitItem())
        }
    }

    @Test
    fun `clear forgets every chat`() = runTest {
        val tracker = TypingTracker()
        tracker.onTyping("c1", "u2", active = true)
        tracker.onTyping("c2", "u3", active = true)
        tracker.clear()

        tracker.observe("c1").test { assertEquals(emptySet<String>(), awaitItem()) }
        tracker.observe("c2").test { assertEquals(emptySet<String>(), awaitItem()) }
    }
}

/**
 * Presence. "Unknown" is a first-class answer here: presence only travels to the
 * peers of a user's direct chats, so for everyone else this client genuinely has
 * no idea — and saying nothing beats rendering a confident "offline".
 */
class PresenceTrackerTest {

    @Test
    fun `reports null for a user we have heard nothing about`() = runTest {
        PresenceTracker().observe("u2").test {
            assertNull(awaitItem())
        }
    }

    @Test
    fun `records that a user is online`() = runTest {
        val tracker = PresenceTracker()
        tracker.onPresence("u2", online = true, lastSeenMs = 1_000)

        tracker.observe("u2").test {
            val presence = awaitItem()
            assertNotNull(presence)
            assertTrue(presence!!.online)
            assertEquals(1_000L, presence.lastSeenMs)
        }
    }

    @Test
    fun `records that a user went offline`() = runTest {
        val tracker = PresenceTracker()
        tracker.onPresence("u2", online = true, lastSeenMs = 1_000)
        tracker.onPresence("u2", online = false, lastSeenMs = 2_000)

        tracker.observe("u2").test {
            val presence = awaitItem()
            assertFalse(presence!!.online)
            assertEquals(2_000L, presence.lastSeenMs)
        }
    }

    /**
     * A presence frame need not carry a timestamp — the server publishes the flag
     * and not always the clock. Storing 0 would render "last seen 1 January 1970";
     * the moment we *learned* it is a truthful stand-in.
     */
    @Test
    fun `falls back to the previously known last-seen when none is reported`() = runTest {
        val tracker = PresenceTracker()
        tracker.onPresence("u2", online = true, lastSeenMs = 1_700_000)
        tracker.onPresence("u2", online = false, lastSeenMs = 0)

        tracker.observe("u2").test {
            assertEquals(1_700_000L, awaitItem()!!.lastSeenMs)
        }
    }

    @Test
    fun `falls back to now when nothing has ever been reported`() = runTest {
        val before = System.currentTimeMillis()
        val tracker = PresenceTracker()
        tracker.onPresence("u2", online = false, lastSeenMs = 0)

        tracker.observe("u2").test {
            val lastSeen = awaitItem()!!.lastSeenMs
            assertTrue("expected a plausible now, got $lastSeen", lastSeen >= before)
        }
    }

    @Test
    fun `ignores a frame with no user id`() = runTest {
        val tracker = PresenceTracker()
        tracker.onPresence("", online = true, lastSeenMs = 1)

        tracker.observe("").test {
            assertNull(awaitItem())
        }
    }

    @Test
    fun `keeps users independent`() = runTest {
        val tracker = PresenceTracker()
        tracker.onPresence("u2", online = true, lastSeenMs = 1)
        tracker.onPresence("u3", online = false, lastSeenMs = 2)

        tracker.observe("u2").test { assertTrue(awaitItem()!!.online) }
        tracker.observe("u3").test { assertFalse(awaitItem()!!.online) }
    }

    @Test
    fun `emits a change to observers`() = runTest {
        val tracker = PresenceTracker()
        tracker.observe("u2").test {
            assertNull(awaitItem())
            tracker.onPresence("u2", online = true, lastSeenMs = 1)
            assertTrue(awaitItem()!!.online)
        }
    }

    @Test
    fun `does not re-emit an unchanged presence`() = runTest {
        // Presence frames repeat on every heartbeat; re-emitting would recompose
        // every chat header several times a minute for no visible change.
        val tracker = PresenceTracker()
        tracker.onPresence("u2", online = true, lastSeenMs = 1)

        tracker.observe("u2").test {
            assertNotNull(awaitItem())
            tracker.onPresence("u2", online = true, lastSeenMs = 1)
            expectNoEvents()
        }
    }

    @Test
    fun `clear forgets everyone`() = runTest {
        // A cached "online" that outlived the app's last connection is a lie told
        // with confidence, so logout has to drop it.
        val tracker = PresenceTracker()
        tracker.onPresence("u2", online = true, lastSeenMs = 1)
        tracker.clear()

        tracker.observe("u2").test {
            assertNull(awaitItem())
        }
    }
}
