package com.synapse.messenger.data.sync

import com.synapse.messenger.domain.model.UserPresence
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update

/**
 * Who is online, in memory only.
 *
 * Presence is ephemeral and per user: the server keeps it in Redis behind a TTL and
 * publishes a change to the bus, so there is nothing here worth persisting — a cached
 * "online" that outlived the app's last connection would be a lie told with
 * confidence.
 *
 * Unknown is a first-class answer. [observe] emits null for anyone we have heard
 * nothing about, and the UI shows nothing at all rather than guessing "offline":
 * presence only travels to the peers of a user's direct chats, so for everyone else
 * this client legitimately has no idea — and saying so beats inventing an answer.
 */
@Singleton
class PresenceTracker @Inject constructor() {

    private val state = MutableStateFlow<Map<String, UserPresence>>(emptyMap())

    fun onPresence(userId: String, online: Boolean, lastSeenMs: Long) {
        if (userId.isEmpty()) return
        state.update { current ->
            current + (
                userId to UserPresence(
                    userId = userId,
                    online = online,
                    // A presence frame that reports no timestamp still tells us WHEN we
                    // learned it, which is all a "last seen" needs.
                    lastSeenMs = lastSeenMs.takeIf { it > 0 }
                        ?: current[userId]?.lastSeenMs
                        ?: System.currentTimeMillis(),
                )
                )
        }
    }

    fun observe(userId: String): Flow<UserPresence?> =
        state.map { it[userId] }.distinctUntilChanged()

    fun clear() = state.update { emptyMap() }
}
