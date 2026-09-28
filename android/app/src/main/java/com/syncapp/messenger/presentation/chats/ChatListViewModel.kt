package com.syncapp.messenger.presentation.chats

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.data.media.MediaUrlCache
import com.syncapp.messenger.domain.model.Chat
import com.syncapp.messenger.domain.model.ChatFlags
import com.syncapp.messenger.domain.repository.AuthRepository
import com.syncapp.messenger.domain.repository.ChatRepository
import com.syncapp.messenger.domain.repository.ConnectionStatus
import com.syncapp.messenger.domain.usecase.RefreshChatsUseCase
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class ChatListUiState(
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    val error: AppError? = null,
)

@HiltViewModel
class ChatListViewModel @Inject constructor(
    private val chatRepository: ChatRepository,
    authRepository: AuthRepository,
    private val refreshChats: RefreshChatsUseCase,
    private val mediaUrls: MediaUrlCache,
) : ViewModel() {

    /**
     * The list comes straight from the local database, so it renders offline and on
     * a cold start before anything has connected. Which is not a nicety here: the
     * protocol has no "list my chats" request, so this cache is the only enumeration
     * of the user's conversations that exists on the device.
     */
    val chats: StateFlow<List<Chat>> = chatRepository.observeChats()
        // Avatar URLs are resolved as rows appear, through a shared app-scoped cache:
        // a media ref has to be exchanged for a signed URL, and the same person shows
        // up on several screens.
        .onEach { rows -> mediaUrls.requestAll(rows.map { it.peerAvatarRef }) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    /**
     * The archived pile: a separate query, not a filter over [chats].
     *
     * [chats] already excludes archived rows — that is what archiving is — so the count
     * has to come from somewhere, and it is read unconditionally so the entry point can
     * show a number before anybody opens it.
     */
    val archivedChats: StateFlow<List<Chat>> = chatRepository.observeArchivedChats()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    val avatarUrls: StateFlow<Map<String, String>> = mediaUrls.urls

    val connection: StateFlow<ConnectionStatus> = authRepository.connection

    private val _state = MutableStateFlow(ChatListUiState())
    val state: StateFlow<ChatListUiState> = _state.asStateFlow()

    init {
        // A first refresh on open, and the loading flag drops either way: an empty
        // list from a failed refresh is still an answer, not a spinner forever.
        viewModelScope.launch {
            val outcome = refreshChats()
            _state.update {
                it.copy(
                    loading = false,
                    error = (outcome as? Outcome.Failure)?.error,
                )
            }
        }
    }

    fun refresh() {
        if (_state.value.refreshing) return
        _state.update { it.copy(refreshing = true, error = null) }
        viewModelScope.launch {
            val outcome = refreshChats()
            _state.update {
                it.copy(refreshing = false, error = (outcome as? Outcome.Failure)?.error)
            }
        }
    }

    /**
     * Mute for a duration, or unmute with [durationMs] = null.
     *
     * A duration rather than a switch, because the server stores a DEADLINE — and it
     * stores one because "mute for eight hours" is what muting almost always means. The
     * boolean this replaces could only say "forever", which is why nothing ever set it.
     */
    fun mute(chat: Chat, durationMs: Long?) {
        applyFlags(
            chat,
            chat.flags.copy(
                mutedUntil = durationMs?.let { System.currentTimeMillis() + it } ?: 0,
            ),
        )
    }

    fun togglePin(chat: Chat) {
        applyFlags(chat, chat.flags.copy(pinned = !chat.flags.pinned))
    }

    fun setArchived(chat: Chat, archived: Boolean) {
        applyFlags(
            chat,
            chat.flags.copy(
                archived = archived,
                // Archiving unpins. Leaving the pin would put the row at the top of a list
                // the user has just said they do not want to look at.
                pinned = if (archived) false else chat.flags.pinned,
            ),
        )
    }

    /**
     * Every write sends all three values, carried over from the row on screen.
     *
     * The wire has no field presence, so "change only this one" cannot be expressed —
     * which means the other two have to be read from somewhere, and reading them off the
     * chat the user is acting on is the only place they are certainly current.
     */
    private fun applyFlags(chat: Chat, flags: ChatFlags) {
        viewModelScope.launch {
            val outcome = chatRepository.setChatFlags(chat.id, flags)
            // Reported rather than swallowed: the local write is optimistic, so a failure
            // leaves the row looking changed until the next enumeration corrects it.
            if (outcome is Outcome.Failure) {
                _state.update { it.copy(error = outcome.error) }
            }
        }
    }

    fun dismissError() = _state.update { it.copy(error = null) }
}
