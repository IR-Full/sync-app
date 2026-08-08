package com.synapse.messenger.presentation.chats

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.synapse.messenger.core.AppError
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.data.media.MediaUrlCache
import com.synapse.messenger.domain.model.Chat
import com.synapse.messenger.domain.repository.AuthRepository
import com.synapse.messenger.domain.repository.ChatRepository
import com.synapse.messenger.domain.repository.ConnectionStatus
import com.synapse.messenger.domain.usecase.RefreshChatsUseCase
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
    chatRepository: ChatRepository,
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

    fun dismissError() = _state.update { it.copy(error = null) }
}
