package com.synapse.messenger.data.sync

import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.update

/**
 * Who is typing, in memory only.
 *
 * Typing is explicitly ephemeral in this protocol: the gateway classifies TYPING
 * into a droppable QoS lane and throttles it to roughly one frame per two seconds
 * per chat, and there is no "stopped typing" guarantee at all — the frame carrying
 * `active = false` is exactly as droppable as the one that started it. So each
 * signal is stored with an expiry and forgotten on its own, rather than trusted to
 * be turned off.
 */
@Singleton
class TypingTracker @Inject constructor() {

    private val state = MutableStateFlow<Map<String, Map<String, Long>>>(emptyMap())

    fun onTyping(chatId: String, userId: String, active: Boolean) {
        if (chatId.isEmpty() || userId.isEmpty()) return
        state.update { current ->
            val forChat = current[chatId].orEmpty().toMutableMap()
            if (active) {
                forChat[userId] = System.currentTimeMillis() + TYPING_TTL_MS
            } else {
                forChat.remove(userId)
            }
            current + (chatId to forChat)
        }
    }

    /**
     * Who is typing in [chatId] right now.
     *
     * The expiry is applied on read AND re-evaluated on a ticker: without the
     * ticker an indicator would stay on screen until the next unrelated typing
     * signal happened to arrive, which is precisely the case where none will.
     */
    fun observe(chatId: String): Flow<Set<String>> = combine(state, ticker()) { current, _ ->
        val now = System.currentTimeMillis()
        current[chatId].orEmpty().filterValues { it > now }.keys
    }.distinctUntilChanged()

    private fun ticker(): Flow<Unit> = flow {
        while (true) {
            emit(Unit)
            delay(TICK_MS)
        }
    }

    fun clear() = state.update { emptyMap() }

    private companion object {
        /**
         * Long enough to survive the server's own typing throttle (one frame per two
         * seconds per chat), short enough that a dropped "stopped" frame does not leave
         * the indicator stuck.
         */
        const val TYPING_TTL_MS = 6_000L
        const val TICK_MS = 1_500L
    }
}
