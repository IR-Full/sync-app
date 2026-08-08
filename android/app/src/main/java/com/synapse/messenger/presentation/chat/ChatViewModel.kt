package com.synapse.messenger.presentation.chat

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.synapse.messenger.core.AppError
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.domain.model.Chat
import com.synapse.messenger.domain.model.ChatTarget
import com.synapse.messenger.domain.model.Message
import com.synapse.messenger.domain.model.MessageStatus
import com.synapse.messenger.domain.repository.AuthRepository
import com.synapse.messenger.domain.repository.ChatRepository
import com.synapse.messenger.domain.repository.ConnectionStatus
import com.synapse.messenger.domain.repository.MediaRepository
import com.synapse.messenger.domain.repository.MessageRepository
import com.synapse.messenger.domain.repository.UserRepository
import com.synapse.messenger.domain.usecase.LoadOlderMessagesUseCase
import com.synapse.messenger.domain.usecase.MarkChatReadUseCase
import com.synapse.messenger.domain.usecase.OpenChatUseCase
import com.synapse.messenger.domain.usecase.SendMessageUseCase
import com.synapse.messenger.presentation.navigation.Routes
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class ChatUiState(
    val loadingOlder: Boolean = false,
    val sending: Boolean = false,
    val hasMoreHistory: Boolean = true,
    val error: AppError? = null,
)

@HiltViewModel
class ChatViewModel @Inject constructor(
    savedState: SavedStateHandle,
    private val chatRepository: ChatRepository,
    private val messageRepository: MessageRepository,
    private val mediaRepository: MediaRepository,
    private val userRepository: UserRepository,
    private val openChat: OpenChatUseCase,
    private val sendMessage: SendMessageUseCase,
    private val loadOlder: LoadOlderMessagesUseCase,
    private val markRead: MarkChatReadUseCase,
    authRepository: AuthRepository,
) : ViewModel() {

    private val routeChatId: String? = savedState.get<String>(Routes.CHAT_ARG_ID)?.takeIf { it.isNotEmpty() }
    private val routePeer: String? = savedState.get<String>(Routes.CHAT_ARG_PEER)?.takeIf { it.isNotEmpty() }

    /**
     * Which chat this screen is showing.
     *
     * A direct chat opened on someone we have never messaged has no id yet — the
     * gateway assigns one when the first message lands — so until then the screen
     * works against a `"@username"` key and switches to the real id the moment it
     * exists. Everything below observes this, so nothing else has to know.
     */
    val chatKey: StateFlow<String> = when {
        routeChatId != null -> flowOf(routeChatId)
        routePeer != null -> chatRepository.observeResolvedDirectChatId(routePeer)
            .map { resolved -> resolved ?: "@$routePeer" }
        else -> flowOf("")
    }.stateIn(viewModelScope, SharingStarted.Eagerly, routeChatId ?: routePeer?.let { "@$it" } ?: "")

    val connection: StateFlow<ConnectionStatus> = authRepository.connection

    val chat: StateFlow<Chat?> = chatKey
        .flatMapLatest { key -> if (key.isEmpty()) flowOf(null) else chatRepository.observeChat(key) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)

    /**
     * The transcript.
     *
     * Read state is derived from the other members' read cursor rather than stored per
     * message: the cursor is monotonic, so a message backfilled after the receipt
     * arrived is still correctly shown as read.
     */
    val messages: StateFlow<List<Message>> = chatKey
        .flatMapLatest { key ->
            if (key.isEmpty()) {
                flowOf(emptyList())
            } else {
                combine(
                    messageRepository.observeMessages(key),
                    messageRepository.observeOthersReadSeq(key),
                ) { rows, readSeq ->
                    rows.map { message ->
                        if (message.isOutgoing &&
                            message.status == MessageStatus.SENT &&
                            message.seq in 1..readSeq
                        ) {
                            message.copy(status = MessageStatus.READ)
                        } else {
                            message
                        }
                    }
                }
            }
        }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    val typingUsers: StateFlow<List<String>> = chatKey
        .flatMapLatest { key ->
            if (key.isEmpty()) flowOf(emptySet()) else messageRepository.observeTyping(key)
        }
        .map { it.toList() }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    /** Labels for the people in this chat, so bubbles can be attributed in a group. */
    val senderLabels: StateFlow<Map<String, String>> = userRepository.observeKnownUsers()
        .map { users -> users.associateBy({ it.userId }, { it.displayLabel }) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyMap())

    private val _mediaUrls = MutableStateFlow<Map<String, String>>(emptyMap())

    /** Signed, expiring download URLs, fetched on demand as bubbles come into view. */
    val mediaUrls: StateFlow<Map<String, String>> = _mediaUrls.asStateFlow()

    private val _state = MutableStateFlow(ChatUiState())
    val state: StateFlow<ChatUiState> = _state.asStateFlow()

    private var draft = MutableStateFlow("")
    val input: StateFlow<String> = draft.asStateFlow()

    init {
        viewModelScope.launch {
            chat.collect { current ->
                _state.update { it.copy(hasMoreHistory = current?.hasMoreHistory ?: false) }
            }
        }
        viewModelScope.launch {
            // Opening on a handle has to resolve first. A conversation with this person
            // may already exist server-side — from another device, or from before a
            // reinstall — and only the resolution learns its id, and with it the history
            // that would otherwise stay invisible.
            val resolved = when {
                routeChatId != null -> routeChatId
                routePeer != null ->
                    (openChat(null, routePeer).getOrNull() as? ChatTarget.Existing)?.chatId
                else -> null
            }
            // A chat opened from the list may hold nothing but the message that
            // announced it, so pull its newest page on entry.
            if (resolved != null) chatRepository.refresh(resolved)
        }
    }

    fun onInputChange(value: String) {
        draft.value = value
        // Typing is throttled in the repository to match the gateway's own limit;
        // anything faster would be relayed to nobody.
        messageRepository.sendTyping(chatKey.value, active = value.isNotEmpty())
    }

    fun send() {
        val text = draft.value
        if (text.isBlank()) return
        val target = targetOf(chatKey.value) ?: return
        draft.value = ""
        _state.update { it.copy(sending = true, error = null) }
        viewModelScope.launch {
            val outcome = sendMessage(target, text)
            _state.update {
                it.copy(sending = false, error = (outcome as? Outcome.Failure)?.error)
            }
        }
    }

    fun sendAttachment(bytes: ByteArray, filename: String, mime: String) {
        val target = targetOf(chatKey.value) ?: return
        _state.update { it.copy(sending = true, error = null) }
        viewModelScope.launch {
            val outcome = messageRepository.sendAttachment(target, bytes, filename, mime)
            _state.update {
                it.copy(sending = false, error = (outcome as? Outcome.Failure)?.error)
            }
        }
    }

    fun retry(messageId: String) {
        viewModelScope.launch { messageRepository.retry(messageId) }
    }

    fun loadOlderMessages() {
        val current = chat.value ?: return
        if (_state.value.loadingOlder || !current.hasMoreHistory) return
        _state.update { it.copy(loadingOlder = true) }
        viewModelScope.launch {
            val outcome = loadOlder(current)
            _state.update {
                it.copy(
                    loadingOlder = false,
                    hasMoreHistory = (outcome as? Outcome.Success)?.value ?: it.hasMoreHistory,
                    error = (outcome as? Outcome.Failure)?.error,
                )
            }
        }
    }

    /** Called when the transcript is on screen: everything visible has been read. */
    fun markVisibleRead() {
        val key = chatKey.value
        if (key.isEmpty()) return
        val newest = messages.value.maxOfOrNull { it.seq } ?: return
        if (newest <= 0) return
        viewModelScope.launch { markRead(key, newest) }
    }

    fun requestMedia(mediaRef: String) {
        if (mediaRef.isEmpty() || _mediaUrls.value.containsKey(mediaRef)) return
        viewModelScope.launch {
            when (val outcome = mediaRepository.downloadUrl(mediaRef)) {
                is Outcome.Success -> _mediaUrls.update { it + (mediaRef to outcome.value) }
                is Outcome.Failure -> Unit // a missing preview is not worth an error banner
            }
        }
    }

    fun dismissError() = _state.update { it.copy(error = null) }

    private fun targetOf(key: String): ChatTarget? = when {
        key.isEmpty() -> null
        key.startsWith("@") -> ChatTarget.DirectPeer(key.removePrefix("@"))
        else -> ChatTarget.Existing(key)
    }
}
