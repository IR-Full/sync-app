package com.syncapp.messenger.domain.usecase

import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.Chat
import com.syncapp.messenger.domain.model.ChatFlags
import com.syncapp.messenger.domain.model.ChatKind
import com.syncapp.messenger.domain.model.ChatTarget
import com.syncapp.messenger.domain.model.Message
import com.syncapp.messenger.domain.model.MessageAttachment
import com.syncapp.messenger.domain.model.UserPresence
import com.syncapp.messenger.domain.model.UserSummary
import com.syncapp.messenger.domain.repository.ChatRepository
import com.syncapp.messenger.domain.repository.MediaRepository
import com.syncapp.messenger.domain.repository.MessageRepository
import com.syncapp.messenger.domain.repository.UserRepository
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.flowOf

/**
 * Hand-written repository doubles.
 *
 * No mocking library is on the test classpath, and for these interfaces that is
 * an advantage rather than a workaround: a use case's whole job is *which* calls
 * it makes and in what order, so a fake that records calls tests the thing that
 * matters, while a mock's argument matchers would only restate the
 * implementation.
 *
 * Methods a given test does not exercise fail loudly instead of returning a
 * default, so a use case that started calling something new cannot pass quietly.
 */

class FakeChatRepository : ChatRepository {
    /** Every method call, in order, as "name(args)". */
    val calls = mutableListOf<String>()

    var refreshAllResult: Outcome<Unit> = Outcome.Success(Unit)
    var createGroupResult: Outcome<Chat> = Outcome.Success(
        Chat(id = "c1", kind = ChatKind.GROUP, title = "Team"),
    )
    var joinResult: Outcome<String> = Outcome.Success("c1")
    var openDirectResult: Outcome<ChatTarget> = Outcome.Success(ChatTarget.Existing("c1"))

    /** What createGroup was actually handed, after the use case normalised it. */
    var lastCreateGroupTitle: String? = null
    var lastCreateGroupMembers: List<String>? = null
    var lastCreateGroupKind: ChatKind? = null
    var lastJoinArgument: String? = null
    var lastOpenDirectArgument: String? = null

    var createSecretResult: Outcome<Chat> = Outcome.Success(
        Chat(id = "s1", kind = ChatKind.SECRET, title = "@bob"),
    )
    var lastCreateSecretPeer: String? = null
    var setFlagsResult: Outcome<ChatFlags>? = null
    var lastSetFlags: Pair<String, ChatFlags>? = null

    override fun observeChats(): Flow<List<Chat>> = flowOf(emptyList())

    override fun observeArchivedChats(): Flow<List<Chat>> = flowOf(emptyList())

    override fun observeChat(chatId: String): Flow<Chat?> = flowOf(null)

    override suspend fun createSecretChat(peer: String): Outcome<Chat> {
        calls += "createSecretChat($peer)"
        lastCreateSecretPeer = peer
        return createSecretResult
    }

    override suspend fun setChatFlags(chatId: String, flags: ChatFlags): Outcome<ChatFlags> {
        calls += "setChatFlags($chatId)"
        lastSetFlags = chatId to flags
        // Echoes the request unless a test wants to model the server clamping it — which
        // it does for an expired mute deadline, and that difference is worth being able
        // to exercise.
        return setFlagsResult ?: Outcome.Success(flags)
    }

    override suspend fun refreshAll(): Outcome<Unit> {
        calls += "refreshAll()"
        return refreshAllResult
    }

    override suspend fun refresh(chatId: String): Outcome<Unit> {
        calls += "refresh($chatId)"
        return Outcome.Success(Unit)
    }

    override suspend fun createGroup(
        title: String,
        memberRefs: List<String>,
        kind: ChatKind,
    ): Outcome<Chat> {
        calls += "createGroup($title)"
        lastCreateGroupTitle = title
        lastCreateGroupMembers = memberRefs
        lastCreateGroupKind = kind
        return createGroupResult
    }

    override suspend fun join(codeOrHandle: String): Outcome<String> {
        calls += "join($codeOrHandle)"
        lastJoinArgument = codeOrHandle
        return joinResult
    }

    override suspend fun openDirectChat(username: String): Outcome<ChatTarget> {
        calls += "openDirectChat($username)"
        lastOpenDirectArgument = username
        return openDirectResult
    }

    override fun observeResolvedDirectChatId(username: String): Flow<String?> = emptyFlow()
}

class FakeUserRepository : UserRepository {
    val calls = mutableListOf<String>()

    var syncContactsResult: Outcome<Unit> = Outcome.Success(Unit)
    var fetchProfileResult: Outcome<UserSummary> = Outcome.Success(UserSummary(userId = "u2"))
    var lastFetchProfileTarget: String? = null

    override fun observeKnownUsers(): Flow<List<UserSummary>> = flowOf(emptyList())

    override fun observeUser(userId: String): Flow<UserSummary?> = flowOf(null)

    override fun observePresence(userId: String): Flow<UserPresence?> = flowOf(null)

    override suspend fun fetchProfile(target: String): Outcome<UserSummary> {
        calls += "fetchProfile($target)"
        lastFetchProfileTarget = target
        return fetchProfileResult
    }

    override suspend fun updateMyProfile(
        displayName: String?,
        avatarRef: String?,
        clearAvatar: Boolean,
    ): Outcome<UserSummary> = error("updateMyProfile is not part of this test")

    override suspend fun refreshMyProfile(): Outcome<UserSummary> =
        error("refreshMyProfile is not part of this test")

    override suspend fun syncContacts(): Outcome<Unit> {
        calls += "syncContacts()"
        return syncContactsResult
    }
}

class FakeMessageRepository : MessageRepository {
    val calls = mutableListOf<String>()

    var sendTextResult: Outcome<Unit> = Outcome.Success(Unit)
    var loadOlderResult: Outcome<Boolean> = Outcome.Success(true)

    var lastSendTarget: ChatTarget? = null
    var lastSendText: String? = null
    var lastSendReplyTo: String? = null
    var lastLoadOlderChatId: String? = null
    var lastLoadOlderPageSize: Int? = null
    var lastMarkReadSeq: Long? = null
    var lastTypingActive: Boolean? = null

    override fun observeMessages(chatId: String): Flow<List<Message>> = flowOf(emptyList())

    override fun observeOthersReadSeq(chatId: String): Flow<Long> = flowOf(0)

    override fun observeOthersDeliveredSeq(chatId: String): Flow<Long> = flowOf(0)

    override fun observeTyping(chatId: String): Flow<Set<String>> = flowOf(emptySet())

    override suspend fun sendText(
        target: ChatTarget,
        text: String,
        replyTo: String?,
    ): Outcome<Unit> {
        calls += "sendText(${target.ref})"
        lastSendTarget = target
        lastSendText = text
        lastSendReplyTo = replyTo
        return sendTextResult
    }

    override suspend fun sendAttachment(
        target: ChatTarget,
        bytes: ByteArray,
        filename: String,
        mime: String,
        caption: String,
    ): Outcome<Unit> = error("sendAttachment is not part of this test")

    override suspend fun retry(messageId: String): Outcome<Unit> =
        error("retry is not part of this test")

    override suspend fun loadOlder(chatId: String, pageSize: Int): Outcome<Boolean> {
        calls += "loadOlder($chatId, $pageSize)"
        lastLoadOlderChatId = chatId
        lastLoadOlderPageSize = pageSize
        return loadOlderResult
    }

    override suspend fun markRead(chatId: String, upToSeq: Long) {
        calls += "markRead($chatId, $upToSeq)"
        lastMarkReadSeq = upToSeq
    }

    override fun sendTyping(chatId: String, active: Boolean) {
        calls += "sendTyping($chatId, $active)"
        lastTypingActive = active
    }

    override suspend fun flushOutbox() {
        calls += "flushOutbox()"
    }
}

class FakeMediaRepository : MediaRepository {
    val requested = mutableListOf<String>()

    var downloadUrlResult: (String) -> Outcome<String> = { ref ->
        Outcome.Success("https://media.test/$ref")
    }

    override suspend fun downloadUrl(mediaRef: String): Outcome<String> {
        requested += mediaRef
        return downloadUrlResult(mediaRef)
    }

    override suspend fun upload(
        bytes: ByteArray,
        filename: String,
        mime: String,
    ): Outcome<MessageAttachment> = error("upload is not part of this test")
}

/** Shorthand for the failure cases, which every use case has to propagate. */
fun <T> failure(error: AppError = AppError.Offline): Outcome<T> = Outcome.Failure(error)
