package com.synapse.messenger.presentation.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.synapse.messenger.core.AppError
import com.synapse.messenger.domain.usecase.AuthResult
import com.synapse.messenger.domain.usecase.CredentialProblem
import com.synapse.messenger.domain.usecase.LoginUseCase
import com.synapse.messenger.domain.usecase.RegisterUseCase
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
) {
    val canSubmit: Boolean get() = !submitting && username.isNotBlank() && password.isNotBlank()
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

    /**
     * Login and registration are separate submissions rather than one adaptive
     * button: the gateway treats them as distinct intents and will not create an
     * account on a failed login, so an ambiguous UI would promise something the
     * protocol refuses to do.
     */
    fun onModeChange(mode: AuthMode) = _state.update {
        it.copy(mode = mode, validation = null, error = null)
    }

    fun submit() {
        val current = _state.value
        if (!current.canSubmit) return
        _state.update { it.copy(submitting = true, validation = null, error = null) }
        viewModelScope.launch {
            val result = when (current.mode) {
                AuthMode.LOGIN -> login(current.username, current.password)
                AuthMode.REGISTER -> register(current.username, current.password)
            }
            when (result) {
                // Navigation is driven by the session, not from here: the root observes
                // it, so a session restored from disk and one just created take the
                // same path.
                is AuthResult.Success -> _state.update { it.copy(submitting = false) }
                is AuthResult.Invalid ->
                    _state.update { it.copy(submitting = false, validation = result.problem) }
                is AuthResult.Failed ->
                    _state.update { it.copy(submitting = false, error = result.error) }
            }
        }
    }
}
