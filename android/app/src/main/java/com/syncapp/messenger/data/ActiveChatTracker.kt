package com.syncapp.messenger.data

import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Which conversation is on screen right now, or "" for none.
 *
 * Only the notification path needs this, and it needs it for a decision the
 * server cannot make: the gateway pushes to every device that is not connected
 * *at that instant*, and has no idea that one of them is showing the very chat
 * the message belongs to. Posting a notification for a message the user is
 * watching arrive is noise.
 *
 * Deliberately app-scoped rather than carried through the navigation graph: the
 * FCM service is constructed by the framework with no access to it.
 */
@Singleton
class ActiveChatTracker @Inject constructor() {
    private val _chatId = MutableStateFlow("")
    val chatId: StateFlow<String> = _chatId.asStateFlow()

    fun setActive(chatId: String) {
        _chatId.value = chatId
    }

    fun clear(chatId: String) {
        // Compare before clearing: screens are disposed after the next one appears,
        // so an unconditional clear would wipe the chat that just opened.
        if (_chatId.value == chatId) _chatId.value = ""
    }
}
