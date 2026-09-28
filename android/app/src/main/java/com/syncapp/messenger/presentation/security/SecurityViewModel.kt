package com.syncapp.messenger.presentation.security

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.TwoFactorSetup
import com.syncapp.messenger.domain.model.TwoFactorState
import com.syncapp.messenger.domain.repository.AccountSecurityRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class SecurityState(
    /**
     * Null means "the server has not said", which is NOT the same as "off".
     *
     * A gateway too old to answer TOTP_STATE leaves it null, and the screen says so
     * rather than drawing a toggle that would refuse to work. Conflating the two would
     * invite an enrolment attempt and report the refusal as an error.
     */
    val twoFactor: TwoFactorState? = null,
    val loading: Boolean = true,
    val busy: Boolean = false,
    /** Non-null while enrolment is in progress and awaiting a code. */
    val setup: TwoFactorSetup? = null,
    /**
     * Shown once, after a successful confirmation.
     *
     * Held here rather than re-read on demand, because there is nothing to re-read: the
     * server stores argon2id hashes. If this is cleared before the user writes them
     * down, the codes are gone for good.
     */
    val recoveryCodes: List<String> = emptyList(),
    /** How many OTHER sessions the last password change signed out. */
    val sessionsRevoked: Int? = null,
    val error: AppError? = null,
    /** Local validation, not a server answer: the two new passwords disagreed. */
    val passwordMismatch: Boolean = false,
)

/**
 * Password and second-factor settings.
 *
 * Neither was reachable before. The password could not be changed by any path — so a
 * leaked one made the account permanently compromised rather than temporarily, since
 * revoking every session does not stop whoever knows the password from signing back in —
 * and there was no second factor at all.
 */
@HiltViewModel
class SecurityViewModel @Inject constructor(
    private val security: AccountSecurityRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(SecurityState())
    val state: StateFlow<SecurityState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            when (val result = security.twoFactorState()) {
                is Outcome.Success -> _state.update {
                    it.copy(twoFactor = result.value, loading = false)
                }
                // Left null rather than defaulted to "off": see [SecurityState.twoFactor].
                is Outcome.Failure -> _state.update {
                    it.copy(loading = false, error = result.error)
                }
            }
        }
    }

    fun changePassword(current: String, new: String, confirm: String) {
        if (new != confirm) {
            _state.update { it.copy(passwordMismatch = true, error = null, sessionsRevoked = null) }
            return
        }
        viewModelScope.launch {
            _state.update {
                it.copy(busy = true, error = null, passwordMismatch = false, sessionsRevoked = null)
            }
            when (val result = security.changePassword(current, new)) {
                // The count is surfaced rather than swallowed: a password change is
                // usually a response to suspecting somebody else has access, and "four
                // other devices were signed out" is the confirmation actually wanted.
                is Outcome.Success -> _state.update {
                    it.copy(busy = false, sessionsRevoked = result.value)
                }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = result.error) }
            }
        }
    }

    fun beginTwoFactor() {
        viewModelScope.launch {
            _state.update { it.copy(busy = true, error = null) }
            when (val result = security.beginTwoFactor()) {
                is Outcome.Success -> _state.update { it.copy(busy = false, setup = result.value) }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = result.error) }
            }
        }
    }

    fun confirmTwoFactor(code: String) {
        viewModelScope.launch {
            _state.update { it.copy(busy = true, error = null) }
            when (val result = security.confirmTwoFactor(code)) {
                is Outcome.Success -> _state.update {
                    it.copy(
                        busy = false,
                        twoFactor = result.value,
                        // Captured from THIS reply and nowhere else.
                        recoveryCodes = result.value.recoveryCodes,
                        setup = null,
                    )
                }
                // The enrolment stays open, so a mistyped code can be retried without
                // starting over — which would mint a new secret and invalidate the one
                // already in the authenticator.
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = result.error) }
            }
        }
    }

    fun disableTwoFactor(password: String, code: String) {
        viewModelScope.launch {
            _state.update { it.copy(busy = true, error = null) }
            when (val result = security.disableTwoFactor(password, code)) {
                is Outcome.Success -> _state.update {
                    it.copy(busy = false, twoFactor = result.value, recoveryCodes = emptyList())
                }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = result.error) }
            }
        }
    }

    fun cancelSetup() {
        _state.update { it.copy(setup = null, error = null) }
    }

    fun dismissRecoveryCodes() {
        _state.update { it.copy(recoveryCodes = emptyList()) }
    }

    fun clearError() {
        _state.update { it.copy(error = null, passwordMismatch = false, sessionsRevoked = null) }
    }
}
