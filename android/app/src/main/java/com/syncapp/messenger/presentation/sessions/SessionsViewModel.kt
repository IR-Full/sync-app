package com.syncapp.messenger.presentation.sessions

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.DeviceSession
import com.syncapp.messenger.domain.repository.AuthRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class SessionsState(
    val sessions: List<DeviceSession> = emptyList(),
    val loading: Boolean = true,
    /** The session id currently being ended, so only that row shows a spinner. */
    val revoking: String? = null,
    val error: AppError? = null,
)

/**
 * The list of devices signed in to this account.
 *
 * Worth having for one reason: before the protocol carried these messages,
 * logging out only discarded the token on the device that did it. A phone that
 * was lost rather than logged out kept access until the session expired, and
 * there was no way to see that it still had it, let alone take it away. This
 * screen is the "take it away".
 *
 * Fetched on demand rather than observed: sessions change when a person signs in
 * or out somewhere, which is rare and not worth a subscription, and a stale list
 * here would be worse than no list — it would show a device as ended that is not.
 */
@HiltViewModel
class SessionsViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(SessionsState())
    val state: StateFlow<SessionsState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            when (val result = authRepository.listSessions()) {
                is Outcome.Success -> _state.update {
                    // Current device first, then newest: the one a person is
                    // looking for is either "this phone" or "the thing that just
                    // signed in that I do not recognise".
                    it.copy(
                        sessions = result.value.sortedWith(
                            compareByDescending<DeviceSession> { session -> session.current }
                                .thenByDescending { session -> session.createdAtMs },
                        ),
                        loading = false,
                    )
                }

                is Outcome.Failure -> _state.update {
                    it.copy(loading = false, error = result.error)
                }
            }
        }
    }

    fun revoke(sessionId: String) {
        viewModelScope.launch {
            _state.update { it.copy(revoking = sessionId, error = null) }
            val result = authRepository.revokeSession(sessionId)
            _state.update { it.copy(revoking = null) }
            when (result) {
                // Re-read rather than removing the row locally: the server
                // decides what a revoke actually did, and a list that shows what
                // we hoped for is the failure this screen exists to prevent.
                is Outcome.Success -> refresh()
                is Outcome.Failure -> _state.update { it.copy(error = result.error) }
            }
        }
    }

    /** Ends every session but this one — what a person reaches for after a loss. */
    fun revokeOthers() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            when (val result = authRepository.revokeOtherSessions()) {
                is Outcome.Success -> refresh()
                is Outcome.Failure -> _state.update {
                    it.copy(loading = false, error = result.error)
                }
            }
        }
    }

    fun clearError() {
        _state.update { it.copy(error = null) }
    }
}
