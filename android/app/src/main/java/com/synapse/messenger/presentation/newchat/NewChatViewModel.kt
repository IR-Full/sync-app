package com.synapse.messenger.presentation.newchat

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.synapse.messenger.core.AppError
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.domain.model.ChatKind
import com.synapse.messenger.domain.model.UserSummary
import com.synapse.messenger.domain.repository.UserRepository
import com.synapse.messenger.domain.usecase.CreateGroupChatUseCase
import com.synapse.messenger.domain.usecase.FindUserUseCase
import com.synapse.messenger.domain.usecase.JoinChatUseCase
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

enum class NewChatTab { DIRECT, GROUP, JOIN }

data class NewChatUiState(
    val tab: NewChatTab = NewChatTab.DIRECT,
    val username: String = "",
    val groupTitle: String = "",
    val groupMembers: String = "",
    val asChannel: Boolean = false,
    val inviteCode: String = "",
    val busy: Boolean = false,
    val error: AppError? = null,
)

/** Where the screen should go once something was created or found. */
sealed interface NewChatDestination {
    data class ByPeer(val username: String) : NewChatDestination

    data class ByChatId(val chatId: String) : NewChatDestination
}

@HiltViewModel
class NewChatViewModel @Inject constructor(
    private val findUser: FindUserUseCase,
    private val createGroup: CreateGroupChatUseCase,
    private val joinChat: JoinChatUseCase,
    userRepository: UserRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(NewChatUiState())
    val state: StateFlow<NewChatUiState> = _state.asStateFlow()

    private val _destinations = MutableSharedFlow<NewChatDestination>()
    val destinations: SharedFlow<NewChatDestination> = _destinations.asSharedFlow()

    /**
     * People this device knows.
     *
     * Only those with a username are offered: a contact known by id alone cannot be
     * opened, because addressing a direct chat requires either its chat id or the
     * peer's handle — and CONTACT_SYNC returns neither for someone whose handle we
     * never typed.
     */
    val contacts: StateFlow<List<UserSummary>> = userRepository.observeKnownUsers()
        .map { users -> users.filter { !it.username.isNullOrEmpty() && !it.blocked } }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    fun onTabChange(tab: NewChatTab) = _state.update { it.copy(tab = tab, error = null) }

    fun onUsernameChange(value: String) = _state.update { it.copy(username = value, error = null) }

    fun onGroupTitleChange(value: String) = _state.update { it.copy(groupTitle = value, error = null) }

    fun onGroupMembersChange(value: String) = _state.update { it.copy(groupMembers = value, error = null) }

    fun onAsChannelChange(value: Boolean) = _state.update { it.copy(asChannel = value) }

    fun onInviteCodeChange(value: String) = _state.update { it.copy(inviteCode = value, error = null) }

    /**
     * Looks a person up by handle.
     *
     * The lookup IS a contact add — CONTACT_ADD is the only request that turns a
     * username into a user id — so a successful search also puts them in the address
     * book. A username that does not exist comes back NOT_FOUND, which is what
     * validates it.
     */
    fun findByUsername() {
        val handle = _state.value.username.trim().removePrefix("@")
        if (handle.isEmpty() || _state.value.busy) return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            when (val outcome = findUser(handle)) {
                is Outcome.Success -> {
                    _state.update { it.copy(busy = false, username = "") }
                    _destinations.emit(NewChatDestination.ByPeer(handle))
                }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = outcome.error) }
            }
        }
    }

    fun openContact(user: UserSummary) {
        val handle = user.username ?: return
        viewModelScope.launch { _destinations.emit(NewChatDestination.ByPeer(handle)) }
    }

    fun createGroupChat() {
        val current = _state.value
        if (current.busy) return
        // Members are typed as handles or ids; the gateway resolves both, so no
        // lookup round trip is needed before creating the chat.
        val members = current.groupMembers
            .split(',', ' ', '\n')
            .map { it.trim() }
            .filter { it.isNotEmpty() }
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            val kind = if (current.asChannel) ChatKind.CHANNEL else ChatKind.GROUP
            when (val outcome = createGroup(current.groupTitle, members, kind)) {
                is Outcome.Success -> {
                    _state.update { it.copy(busy = false, groupTitle = "", groupMembers = "") }
                    _destinations.emit(NewChatDestination.ByChatId(outcome.value.id))
                }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = outcome.error) }
            }
        }
    }

    fun join() {
        val current = _state.value
        if (current.busy || current.inviteCode.isBlank()) return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            when (val outcome = joinChat(current.inviteCode)) {
                is Outcome.Success -> {
                    _state.update { it.copy(busy = false, inviteCode = "") }
                    _destinations.emit(NewChatDestination.ByChatId(outcome.value))
                }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = outcome.error) }
            }
        }
    }
}
