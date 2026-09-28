package com.syncapp.messenger.presentation.privacy

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.PrivacySettings
import com.syncapp.messenger.domain.model.Visibility
import com.syncapp.messenger.domain.repository.AuthRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Which of the three settings a row changes. */
enum class PrivacyField { LAST_SEEN, AVATAR, GROUPS }

data class PrivacyState(
    val settings: PrivacySettings = PrivacySettings(),
    val loading: Boolean = true,
    val saving: Boolean = false,
    val error: AppError? = null,
)

/**
 * Who may see last seen, the avatar, and who may add this account to groups.
 *
 * The state here is always the server's answer, never the request. The two can
 * differ — a value this build does not recognise comes back as
 * [Visibility.UNKNOWN] — and echoing the request would tell someone a privacy
 * setting took effect when it had not, which is the one lie this screen must
 * never tell.
 */
@HiltViewModel
class PrivacyViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(PrivacyState())
    val state: StateFlow<PrivacyState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            when (val result = authRepository.privacy()) {
                is Outcome.Success -> _state.update {
                    it.copy(settings = result.value, loading = false)
                }

                is Outcome.Failure -> _state.update {
                    it.copy(loading = false, error = result.error)
                }
            }
        }
    }

    fun set(field: PrivacyField, value: Visibility) {
        val current = _state.value.settings
        val next = when (field) {
            PrivacyField.LAST_SEEN -> current.copy(lastSeen = value)
            PrivacyField.AVATAR -> current.copy(avatar = value)
            PrivacyField.GROUPS -> current.copy(groups = value)
        }
        if (next == current) return

        viewModelScope.launch {
            _state.update { it.copy(saving = true, error = null) }
            when (val result = authRepository.setPrivacy(next)) {
                is Outcome.Success -> _state.update {
                    it.copy(settings = result.value, saving = false)
                }

                // The old value stays on screen. A row that moved and then
                // failed would leave someone believing they had tightened
                // something they had not.
                is Outcome.Failure -> _state.update {
                    it.copy(saving = false, error = result.error)
                }
            }
        }
    }

    fun clearError() {
        _state.update { it.copy(error = null) }
    }
}
