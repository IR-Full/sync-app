package com.synapse.messenger.domain.usecase

import com.synapse.messenger.core.AppError
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.domain.model.Chat
import com.synapse.messenger.domain.model.ChatKind
import com.synapse.messenger.domain.model.ChatTarget
import com.synapse.messenger.domain.model.UserSummary
import com.synapse.messenger.domain.repository.ChatRepository
import com.synapse.messenger.domain.repository.UserRepository
import javax.inject.Inject

/**
 * Refreshes what a pull-to-refresh can refresh.
 *
 * Contacts are pulled alongside the chats because they are what turns a numeric
 * sender id into a name; a chat list refresh that skipped them would look like it
 * had failed to load half the screen.
 */
class RefreshChatsUseCase @Inject constructor(
    private val chatRepository: ChatRepository,
    private val userRepository: UserRepository,
) {
    suspend operator fun invoke(): Outcome<Unit> {
        val chats = chatRepository.refreshAll()
        // Contacts are supporting data: their failure must not fail the refresh.
        userRepository.syncContacts()
        return chats
    }
}

/**
 * Resolves what the chat screen should open.
 *
 * A route may carry a chat id or a username, because a direct chat has no id until
 * its first message — see [ChatRepository.openDirectChat].
 */
class OpenChatUseCase @Inject constructor(
    private val chatRepository: ChatRepository,
) {
    suspend operator fun invoke(chatId: String?, username: String?): Outcome<ChatTarget> = when {
        !chatId.isNullOrEmpty() -> Outcome.Success(ChatTarget.Existing(chatId))
        !username.isNullOrEmpty() -> chatRepository.openDirectChat(username)
        else -> Outcome.Failure(AppError.NotFound(null))
    }
}

/**
 * Finds a person by handle.
 *
 * The lookup and the contact add are the same request in this protocol
 * (CONTACT_ADD is the only one that takes a username and returns an id), so
 * searching for someone necessarily records them. That is a protocol fact worth
 * knowing at the call site, not a decision made here.
 */
class FindUserUseCase @Inject constructor(
    private val userRepository: UserRepository,
) {
    suspend operator fun invoke(username: String): Outcome<UserSummary> =
        userRepository.addContact(username)
}

class CreateGroupChatUseCase @Inject constructor(
    private val chatRepository: ChatRepository,
) {
    /** The server caps titles at 128 characters and initial members at 200. */
    suspend operator fun invoke(
        title: String,
        memberRefs: List<String>,
        kind: ChatKind,
    ): Outcome<Chat> {
        val trimmed = title.trim()
        if (trimmed.isEmpty() || trimmed.length > MAX_TITLE) {
            return Outcome.Failure(AppError.Rejected(0, "title must be 1..$MAX_TITLE characters"))
        }
        val members = memberRefs.map { it.trim() }.filter { it.isNotEmpty() }.distinct()
        if (members.size > MAX_MEMBERS) {
            return Outcome.Failure(AppError.Rejected(0, "too many initial members"))
        }
        return chatRepository.createGroup(trimmed, members, kind)
    }

    private companion object {
        const val MAX_TITLE = 128
        const val MAX_MEMBERS = 200
    }
}

class JoinChatUseCase @Inject constructor(
    private val chatRepository: ChatRepository,
) {
    suspend operator fun invoke(codeOrHandle: String): Outcome<String> {
        val trimmed = codeOrHandle.trim()
        if (trimmed.isEmpty()) return Outcome.Failure(AppError.Rejected(0, "empty invite"))
        return chatRepository.join(trimmed)
    }
}
