package com.synapse.messenger.domain.usecase

import com.synapse.messenger.core.Outcome
import com.synapse.messenger.domain.model.Chat
import com.synapse.messenger.domain.model.ChatTarget
import com.synapse.messenger.domain.repository.MessageRepository
import javax.inject.Inject

/**
 * Sends a message and closes out the composing state.
 *
 * The explicit "stopped typing" matters because the indicator on the other side is
 * driven by an expiry: without it the peer sees "typing…" for a few more seconds
 * *next to the message that just arrived*.
 */
class SendMessageUseCase @Inject constructor(
    private val repository: MessageRepository,
) {
    suspend operator fun invoke(
        target: ChatTarget,
        text: String,
        replyTo: String? = null,
    ): Outcome<Unit> {
        val result = repository.sendText(target, text, replyTo)
        repository.sendTyping(target.ref, active = false)
        return result
    }
}

/**
 * Loads one older page, if there is one.
 *
 * The guard is the point: a chat whose cursor says the server has nothing older
 * must not issue a request every time the user reaches the top of the list.
 */
class LoadOlderMessagesUseCase @Inject constructor(
    private val repository: MessageRepository,
) {
    suspend operator fun invoke(chat: Chat, pageSize: Int = 50): Outcome<Boolean> {
        if (!chat.hasMoreHistory) return Outcome.Success(false)
        return repository.loadOlder(chat.id, pageSize)
    }
}

/**
 * Marks the chat read up to the newest message the user has actually seen.
 *
 * Read state is per chat and monotonic on the server, so sending a lower cursor is
 * a no-op there — but sending one at all costs a frame, hence the local comparison
 * in the repository.
 */
class MarkChatReadUseCase @Inject constructor(
    private val repository: MessageRepository,
) {
    suspend operator fun invoke(chatId: String, upToSeq: Long) = repository.markRead(chatId, upToSeq)
}
