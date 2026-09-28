package com.syncapp.messenger.presentation.secret

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.crypto.TrustVerdict
import com.syncapp.messenger.data.secret.DeviceSafety
import com.syncapp.messenger.data.secret.IdentityChangedException
import com.syncapp.messenger.data.secret.NoSecretDevicesException
import com.syncapp.messenger.data.secret.SecretChatEngine
import com.syncapp.messenger.data.secret.SecretMessageRow
import com.syncapp.messenger.data.secret.SecretTranscriptStore
import com.syncapp.messenger.domain.repository.AuthRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * One end-to-end encrypted conversation.
 *
 * Deliberately not folded into [ChatViewModel]: a secret chat is a different
 * thing, not a mode of the same one. These messages never reach the server in
 * readable form, are not stored anywhere, and exist only on the devices that
 * received them — presenting them in the ordinary transcript would imply a
 * history and a cross-device sync that do not exist.
 */
data class SecretChatState(
    val messages: List<SecretMessageRow> = emptyList(),
    val sending: Boolean = false,
    val error: SecretError? = null,
    /** non-empty while the verification sheet is open */
    val safety: List<DeviceSafety> = emptyList(),
    val loadingSafety: Boolean = false,
)

/** What went wrong, in terms the UI can act on rather than a raw exception. */
enum class SecretError {
    /** The peer has published no prekeys — there is nothing to encrypt to. */
    NO_DEVICES,

    /**
     * A peer device's identity changed. The send was REFUSED rather than sent to
     * the new key, because a reinstall and an attack look identical from here.
     */
    IDENTITY_CHANGED,

    UNKNOWN,
}

@HiltViewModel
class SecretChatViewModel @Inject constructor(
    private val engine: SecretChatEngine,
    private val transcripts: SecretTranscriptStore,
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(SecretChatState())
    val state: StateFlow<SecretChatState> = _state.asStateFlow()

    private var peerUserId: String = ""

    fun open(peerUserId: String) {
        this.peerUserId = peerUserId
        viewModelScope.launch {
            transcripts.transcripts.collect { all ->
                _state.value = _state.value.copy(messages = all[peerUserId].orEmpty())
            }
        }
    }

    fun send(text: String) {
        val trimmed = text.trim()
        if (trimmed.isEmpty() || peerUserId.isEmpty()) return

        viewModelScope.launch {
            _state.value = _state.value.copy(sending = true, error = null)
            val result = runCatching { engine.send(peerUserId, trimmed) }
            _state.value = _state.value.copy(
                sending = false,
                error = result.exceptionOrNull()?.let(::classify),
            )
            // The local echo is appended only on success: showing a message that
            // was refused would tell the user it went out when it did not.
            if (result.isSuccess) {
                transcripts.append(peerUserId, trimmed, outgoing = true)
            } else if (result.exceptionOrNull() is IdentityChangedException) {
                // Open the verification sheet rather than leaving a bare error
                // with no way forward — pinning refusing to send is not a
                // malfunction, but it looks like one without this.
                loadSafetyNumbers()
            }
        }
    }

    /**
     * Fetches the peer's safety numbers.
     *
     * On demand rather than cached: the number is only meaningful at the moment
     * someone is comparing it, and a stale one would show a match against keys
     * that are no longer in use.
     */
    fun loadSafetyNumbers() {
        viewModelScope.launch {
            _state.value = _state.value.copy(loadingSafety = true)
            val self = authRepository.session.first()?.userId.orEmpty()
            val devices = runCatching { engine.safetyNumbers(self, peerUserId) }
                .getOrDefault(emptyList())
            _state.value = _state.value.copy(safety = devices, loadingSafety = false)
        }
    }

    fun dismissSafety() {
        _state.value = _state.value.copy(safety = emptyList())
    }

    /** Records the keys the human just compared, then re-derives the list. */
    fun acceptIdentity(device: DeviceSafety) {
        viewModelScope.launch {
            engine.acceptIdentity(device)
            loadSafetyNumbers()
        }
    }

    fun clearError() {
        _state.value = _state.value.copy(error = null)
    }

    private fun classify(error: Throwable): SecretError = when (error) {
        is NoSecretDevicesException -> SecretError.NO_DEVICES
        is IdentityChangedException -> SecretError.IDENTITY_CHANGED
        else -> SecretError.UNKNOWN
    }
}

/** Whether a device's keys are recorded, unrecorded, or changed. */
val DeviceSafety.isPinned: Boolean get() = verdict is TrustVerdict.Known
val DeviceSafety.hasChanged: Boolean get() = verdict is TrustVerdict.Changed
