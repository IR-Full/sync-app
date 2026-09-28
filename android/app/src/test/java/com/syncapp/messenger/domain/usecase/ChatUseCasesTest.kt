package com.syncapp.messenger.domain.usecase

import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.ChatKind
import com.syncapp.messenger.domain.model.ChatTarget
import com.syncapp.messenger.domain.model.UserSummary
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class RefreshChatsUseCaseTest {

    @Test
    fun `refreshes chats and contacts together`() = runTest {
        // Contacts are what turn a numeric sender id into a name, so a refresh
        // that skipped them would look like half the screen failed to load.
        val chats = FakeChatRepository()
        val users = FakeUserRepository()

        RefreshChatsUseCase(chats, users)()

        assertTrue("chats were not refreshed", chats.calls.contains("refreshAll()"))
        assertTrue("contacts were not synced", users.calls.contains("syncContacts()"))
    }

    @Test
    fun `returns the chat refresh result`() = runTest {
        val chats = FakeChatRepository().apply { refreshAllResult = failure(AppError.Timeout) }

        val result = RefreshChatsUseCase(chats, FakeUserRepository())()

        assertEquals(Outcome.Failure(AppError.Timeout), result)
    }

    /**
     * Contacts are supporting data. A contact sync that fails must not turn a
     * successful chat refresh into an error banner — the chat list is perfectly
     * usable with ids instead of names, and the next sync will fill them in.
     */
    @Test
    fun `a failed contact sync does not fail the refresh`() = runTest {
        val users = FakeUserRepository().apply { syncContactsResult = failure(AppError.Offline) }

        val result = RefreshChatsUseCase(FakeChatRepository(), users)()

        assertEquals(Outcome.Success(Unit), result)
    }

    @Test
    fun `still syncs contacts when the chat refresh failed`() = runTest {
        // The two are independent; one being down is no reason to skip the other.
        val chats = FakeChatRepository().apply { refreshAllResult = failure() }
        val users = FakeUserRepository()

        RefreshChatsUseCase(chats, users)()

        assertTrue(users.calls.contains("syncContacts()"))
    }
}

class OpenChatUseCaseTest {

    @Test
    fun `opens an existing chat by id without asking the server`() = runTest {
        // A chat we already know needs no round trip; going to the network here
        // would put a spinner on every chat open.
        val chats = FakeChatRepository()

        val result = OpenChatUseCase(chats)(chatId = "c1", username = null)

        assertEquals(Outcome.Success(ChatTarget.Existing("c1")), result)
        assertTrue("should not have hit the repository", chats.calls.isEmpty())
    }

    @Test
    fun `resolves a username through the repository`() = runTest {
        // A direct chat has no id until its first message, so a route carrying a
        // handle has to be resolved.
        val chats = FakeChatRepository().apply {
            openDirectResult = Outcome.Success(ChatTarget.DirectPeer("bob"))
        }

        val result = OpenChatUseCase(chats)(chatId = null, username = "bob")

        assertEquals(Outcome.Success(ChatTarget.DirectPeer("bob")), result)
        assertEquals("bob", chats.lastOpenDirectArgument)
    }

    @Test
    fun `prefers the chat id when the route carries both`() = runTest {
        val chats = FakeChatRepository()

        val result = OpenChatUseCase(chats)(chatId = "c1", username = "bob")

        assertEquals(Outcome.Success(ChatTarget.Existing("c1")), result)
        assertTrue(chats.calls.isEmpty())
    }

    @Test
    fun `treats an empty chat id as absent`() = runTest {
        // Navigation arguments arrive as strings; an optional one that was not
        // supplied shows up as "" rather than null.
        val chats = FakeChatRepository().apply {
            openDirectResult = Outcome.Success(ChatTarget.DirectPeer("bob"))
        }

        OpenChatUseCase(chats)(chatId = "", username = "bob")

        assertEquals("bob", chats.lastOpenDirectArgument)
    }

    @Test
    fun `fails when the route carries neither`() = runTest {
        val result = OpenChatUseCase(FakeChatRepository())(chatId = null, username = null)
        assertTrue(result is Outcome.Failure && result.error is AppError.NotFound)
    }

    @Test
    fun `fails when both are empty strings`() = runTest {
        val result = OpenChatUseCase(FakeChatRepository())(chatId = "", username = "")
        assertTrue(result is Outcome.Failure)
    }

    @Test
    fun `propagates a resolution failure`() = runTest {
        val chats = FakeChatRepository().apply {
            openDirectResult = failure(AppError.NotFound("no such user: @bob"))
        }

        val result = OpenChatUseCase(chats)(chatId = null, username = "bob")

        assertEquals(Outcome.Failure(AppError.NotFound("no such user: @bob")), result)
    }
}

class FindUserUseCaseTest {

    @Test
    fun `looks a person up by handle`() = runTest {
        val users = FakeUserRepository().apply {
            fetchProfileResult = Outcome.Success(UserSummary(userId = "u2", username = "bob"))
        }

        val result = FindUserUseCase(users)("bob")

        assertEquals(Outcome.Success(UserSummary(userId = "u2", username = "bob")), result)
    }

    @Test
    fun `addresses the profile lookup with an at-sign`() = runTest {
        // The gateway distinguishes a user id from a handle by the "@" prefix; a
        // bare name would be looked up as an id and never found.
        val users = FakeUserRepository()

        FindUserUseCase(users)("bob")

        assertEquals("@bob", users.lastFetchProfileTarget)
    }

    @Test
    fun `accepts a handle the user typed with an at-sign already`() = runTest {
        // Otherwise the lookup would go out as "@@bob".
        val users = FakeUserRepository()

        FindUserUseCase(users)("@bob")

        assertEquals("@bob", users.lastFetchProfileTarget)
    }

    @Test
    fun `trims whitespace around a pasted handle`() = runTest {
        val users = FakeUserRepository()

        FindUserUseCase(users)("  @bob  ")

        assertEquals("@bob", users.lastFetchProfileTarget)
    }

    @Test
    fun `rejects an empty handle without a round trip`() = runTest {
        // Searching for nothing is a guaranteed NOT_FOUND; spending a frame on it
        // also spends rate-limit budget.
        val users = FakeUserRepository()

        val result = FindUserUseCase(users)("")

        assertTrue(result is Outcome.Failure && result.error is AppError.NotFound)
        assertTrue(users.calls.isEmpty())
    }

    @Test
    fun `rejects a handle that is only whitespace or an at-sign`() = runTest {
        val users = FakeUserRepository()

        assertTrue(FindUserUseCase(users)("   ") is Outcome.Failure)
        assertTrue(FindUserUseCase(users)("@") is Outcome.Failure)
        assertTrue(users.calls.isEmpty())
    }

    @Test
    fun `is a read — it does not add the person to the address book`() = runTest {
        // The lookup used to be smuggled through a contact add, which meant
        // searching for someone silently filed them in your contacts.
        val users = FakeUserRepository()

        FindUserUseCase(users)("bob")

        assertEquals(listOf("fetchProfile(@bob)"), users.calls)
    }

    @Test
    fun `propagates an unknown handle`() = runTest {
        val users = FakeUserRepository().apply {
            fetchProfileResult = failure(AppError.NotFound("no such user: @bob"))
        }

        val result = FindUserUseCase(users)("bob")

        assertEquals(Outcome.Failure(AppError.NotFound("no such user: @bob")), result)
    }
}

class CreateGroupChatUseCaseTest {

    @Test
    fun `creates a group`() = runTest {
        val chats = FakeChatRepository()

        CreateGroupChatUseCase(chats)("Team", listOf("@bob"), ChatKind.GROUP)

        assertEquals("Team", chats.lastCreateGroupTitle)
        assertEquals(listOf("@bob"), chats.lastCreateGroupMembers)
        assertEquals(ChatKind.GROUP, chats.lastCreateGroupKind)
    }

    @Test
    fun `trims the title`() = runTest {
        val chats = FakeChatRepository()

        CreateGroupChatUseCase(chats)("  Team  ", emptyList(), ChatKind.GROUP)

        assertEquals("Team", chats.lastCreateGroupTitle)
    }

    @Test
    fun `rejects an empty title locally`() = runTest {
        // The server enforces this too, but a local rejection gives instant
        // feedback and costs no rate-limit budget.
        val chats = FakeChatRepository()

        val result = CreateGroupChatUseCase(chats)("", emptyList(), ChatKind.GROUP)

        assertTrue(result is Outcome.Failure)
        assertTrue(chats.calls.isEmpty())
    }

    @Test
    fun `rejects a whitespace-only title`() = runTest {
        val chats = FakeChatRepository()

        assertTrue(CreateGroupChatUseCase(chats)("   ", emptyList(), ChatKind.GROUP) is Outcome.Failure)
        assertTrue(chats.calls.isEmpty())
    }

    @Test
    fun `accepts a title exactly at the server limit`() = runTest {
        // 128 is allowed, 129 is not — the boundary is where an off-by-one lives.
        val chats = FakeChatRepository()

        val result = CreateGroupChatUseCase(chats)("t".repeat(128), emptyList(), ChatKind.GROUP)

        assertTrue(result is Outcome.Success)
    }

    @Test
    fun `rejects a title one character past the limit`() = runTest {
        val chats = FakeChatRepository()

        val result = CreateGroupChatUseCase(chats)("t".repeat(129), emptyList(), ChatKind.GROUP)

        assertTrue(result is Outcome.Failure)
        assertTrue(chats.calls.isEmpty())
    }

    @Test
    fun `measures the title after trimming, not before`() = runTest {
        val chats = FakeChatRepository()

        val result = CreateGroupChatUseCase(chats)(
            "  " + "t".repeat(128) + "  ",
            emptyList(),
            ChatKind.GROUP,
        )

        assertTrue(result is Outcome.Success)
    }

    @Test
    fun `drops duplicate members`() = runTest {
        // The same handle twice is a user mistake, and the server would reject the
        // whole request rather than ignore the repeat.
        val chats = FakeChatRepository()

        CreateGroupChatUseCase(chats)("Team", listOf("@bob", "@bob", "@ann"), ChatKind.GROUP)

        assertEquals(listOf("@bob", "@ann"), chats.lastCreateGroupMembers)
    }

    @Test
    fun `drops blank member entries`() = runTest {
        // A comma-separated input box leaves empties behind.
        val chats = FakeChatRepository()

        CreateGroupChatUseCase(chats)("Team", listOf("@bob", "", "  "), ChatKind.GROUP)

        assertEquals(listOf("@bob"), chats.lastCreateGroupMembers)
    }

    @Test
    fun `trims member entries before deduplicating them`() = runTest {
        // Otherwise " @bob" and "@bob" would survive as two distinct members.
        val chats = FakeChatRepository()

        CreateGroupChatUseCase(chats)("Team", listOf(" @bob ", "@bob"), ChatKind.GROUP)

        assertEquals(listOf("@bob"), chats.lastCreateGroupMembers)
    }

    @Test
    fun `accepts exactly the maximum number of members`() = runTest {
        val chats = FakeChatRepository()
        val members = (1..200).map { "@u$it" }

        assertTrue(CreateGroupChatUseCase(chats)("Team", members, ChatKind.GROUP) is Outcome.Success)
    }

    @Test
    fun `rejects one member too many`() = runTest {
        val chats = FakeChatRepository()
        val members = (1..201).map { "@u$it" }

        val result = CreateGroupChatUseCase(chats)("Team", members, ChatKind.GROUP)

        assertTrue(result is Outcome.Failure)
        assertTrue(chats.calls.isEmpty())
    }

    @Test
    fun `counts members after deduplication`() = runTest {
        // 201 entries that are really 200 distinct people is a valid group.
        val chats = FakeChatRepository()
        val members = (1..200).map { "@u$it" } + "@u1"

        assertTrue(CreateGroupChatUseCase(chats)("Team", members, ChatKind.GROUP) is Outcome.Success)
    }

    @Test
    fun `creates a channel when asked for one`() = runTest {
        val chats = FakeChatRepository()

        CreateGroupChatUseCase(chats)("News", emptyList(), ChatKind.CHANNEL)

        assertEquals(ChatKind.CHANNEL, chats.lastCreateGroupKind)
    }

    @Test
    fun `propagates a server rejection`() = runTest {
        val chats = FakeChatRepository().apply {
            createGroupResult = failure(AppError.Forbidden("too many new chats"))
        }

        val result = CreateGroupChatUseCase(chats)("Team", emptyList(), ChatKind.GROUP)

        assertEquals(Outcome.Failure(AppError.Forbidden("too many new chats")), result)
    }
}

class JoinChatUseCaseTest {

    @Test
    fun `joins by invite code`() = runTest {
        val chats = FakeChatRepository().apply { joinResult = Outcome.Success("c9") }

        val result = JoinChatUseCase(chats)("abc123")

        assertEquals(Outcome.Success("c9"), result)
        assertEquals("abc123", chats.lastJoinArgument)
    }

    @Test
    fun `joins by public handle`() = runTest {
        val chats = FakeChatRepository()

        JoinChatUseCase(chats)("@news")

        assertEquals("@news", chats.lastJoinArgument)
    }

    @Test
    fun `trims a pasted invite`() = runTest {
        // Invite codes get copied out of chat messages with stray whitespace.
        val chats = FakeChatRepository()

        JoinChatUseCase(chats)("  abc123  ")

        assertEquals("abc123", chats.lastJoinArgument)
    }

    @Test
    fun `rejects an empty invite without a round trip`() = runTest {
        val chats = FakeChatRepository()

        assertTrue(JoinChatUseCase(chats)("") is Outcome.Failure)
        assertTrue(JoinChatUseCase(chats)("   ") is Outcome.Failure)
        assertTrue(chats.calls.isEmpty())
    }

    @Test
    fun `propagates a rejected invite`() = runTest {
        val chats = FakeChatRepository().apply {
            joinResult = failure(AppError.NotFound("invite expired"))
        }

        val result = JoinChatUseCase(chats)("abc123")

        assertEquals(Outcome.Failure(AppError.NotFound("invite expired")), result)
    }
}
