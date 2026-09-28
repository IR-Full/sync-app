package com.syncapp.messenger.presentation.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.domain.usecase.AuthResult
import com.syncapp.messenger.domain.usecase.CredentialProblem
import com.syncapp.messenger.domain.usecase.LoginUseCase
import com.syncapp.messenger.domain.usecase.RegisterUseCase
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

enum class AuthMode { LOGIN, REGISTER }

data class AuthUiState(
    val mode: AuthMode = AuthMode.LOGIN,
    val username: String = "",
    val password: String = "",
    val submitting: Boolean = false,
    val validation: CredentialProblem? = null,
    val error: AppError? = null,
    /**
     * True once the server has said the credentials are right and a code is needed.
     *
     * A separate flag rather than an error, and the username and password are KEPT: the
     * second attempt has to send all three, and a form that cleared itself would make a
     * successful first step look like a failure.
     */
    val needsSecondFactor: Boolean = false,
    val totpCode: String = "",
) {
    val canSubmit: Boolean get() = !submitting &&
        username.isNotBlank() &&
        password.isNotBlank() &&
        // Once the server has asked, an empty code cannot succeed — so the button is
        // disabled rather than spending an attempt against the account's rate limit.
        (!needsSecondFactor || totpCode.isNotBlank())
}

@HiltViewModel
class AuthViewModel @Inject constructor(
    private val login: LoginUseCase,
    private val register: RegisterUseCase,
) : ViewModel() {

    private val _state = MutableStateFlow(AuthUiState())
    val state: StateFlow<AuthUiState> = _state.asStateFlow()

    fun onUsernameChange(value: String) = _state.update {
        it.copy(username = value, validation = null, error = null)
    }

    fun onPasswordChange(value: String) = _state.update {
        it.copy(password = value, validation = null, error = null)
    }

    fun onTotpCodeChange(value: String) = _state.update {
        it.copy(totpCode = value, error = null)
    }

    /**
     * Login and registration are separate submissions rather than one adaptive
     * button: the gateway treats them as distinct intents and will not create an
     * account on a failed login, so an ambiguous UI would promise something the
     * protocol refuses to do.
     */
    fun onModeChange(mode: AuthMode) = _state.update {
        // The second-factor step belongs to one login attempt: switching to registration
        // and back must not leave the code field demanding input for an account nobody
        // is signing into any more.
        it.copy(
            mode = mode,
            validation = null,
            error = null,
            needsSecondFactor = false,
            totpCode = "",
        )
    }

    fun submit() {
        val current = _state.value
        if (!current.canSubmit) return
        _state.update { it.copy(submitting = true, validation = null, error = null) }
        viewModelScope.launch {
            val result = when (current.mode) {
                AuthMode.LOGIN -> login(current.username, current.password, current.totpCode)
                AuthMode.REGISTER -> register(current.username, current.password)
            }
            when (result) {
                // Navigation is driven by the session, not from here: the root observes
                // it, so a session restored from disk and one just created take the
                // same path.
                is AuthResult.Success -> _state.update { it.copy(submitting = false) }
                is AuthResult.Invalid ->
                    _state.update { it.copy(submitting = false, validation = result.problem) }
                // Not an error. The form grows a field and keeps what was typed.
                AuthResult.NeedsSecondFactor -> _state.update {
                    it.copy(submitting = false, needsSecondFactor = true, error = null)
                }
                is AuthResult.Failed ->
                    // The code is cleared and the prompt STAYS: a wrong code is worth
                    // another try, and dropping back to the password form would look like
                    // the password had been rejected.
                    _state.update {
                        it.copy(submitting = false, error = result.error, totpCode = "")
                    }
            }
        }
    }
}
