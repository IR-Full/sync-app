package com.syncapp.messenger.domain.usecase

import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.Session
import com.syncapp.messenger.domain.repository.AuthRepository
import com.syncapp.messenger.network.protocol.ErrorCode
import javax.inject.Inject

/**
 * What is wrong with the credentials the user typed, before anything is sent.
 *
 * The bounds mirror the server (`internal/auth`: username ≥ 3, password ≥ 6) so a
 * rejection the client can predict never costs a round trip — and never reaches the
 * gateway's per-username brute-force throttle, which counts failed *login* attempts
 * regardless of why they failed.
 */
enum class CredentialProblem { USERNAME_TOO_SHORT, USERNAME_INVALID_CHARS, PASSWORD_TOO_SHORT }

object CredentialRules {
    const val MIN_USERNAME = 3
    const val MIN_PASSWORD = 6

    /**
     * Usernames are lowercased server-side and addressed as `@name` inside the
     * protocol, so a handle with an "@" or whitespace in it could never be typed
     * back — restrict to what can round-trip.
     */
    private val allowed = Regex("^[a-z0-9_.-]+$")

    fun check(username: String, password: String): CredentialProblem? {
        val handle = username.trim().removePrefix("@").lowercase()
        return when {
            handle.length < MIN_USERNAME -> CredentialProblem.USERNAME_TOO_SHORT
            !allowed.matches(handle) -> CredentialProblem.USERNAME_INVALID_CHARS
            password.length < MIN_PASSWORD -> CredentialProblem.PASSWORD_TOO_SHORT
            else -> null
        }
    }

    fun normalize(username: String): String = username.trim().removePrefix("@").lowercase()
}

sealed interface AuthResult {
    /**
     * The credentials were accepted and a second factor is needed.
     *
     * Its own case rather than an error, because the screen's next step is different: it
     * adds a field and keeps what was already typed, instead of clearing the form and
     * claiming the password was wrong.
     */
    data object NeedsSecondFactor : AuthResult

    data class Success(val session: Session) : AuthResult

    data class Invalid(val problem: CredentialProblem) : AuthResult

    data class Failed(val error: AppError) : AuthResult
}

class LoginUseCase @Inject constructor(private val repository: AuthRepository) {
    suspend operator fun invoke(
        username: String,
        password: String,
        totpCode: String = "",
    ): AuthResult {
        CredentialRules.check(username, password)?.let { return AuthResult.Invalid(it) }
        val outcome = repository.login(CredentialRules.normalize(username), password, totpCode)
        return when (outcome) {
            is Outcome.Success -> AuthResult.Success(outcome.value)
            is Outcome.Failure ->
                // A demand for a second factor is NOT a failed login, and reporting it as
                // one would tell somebody their password is wrong when it is not — then
                // leave them with no way in at all, since nothing would ask for the code.
                if (outcome.error is AppError.Rejected &&
                    (outcome.error as AppError.Rejected).code == ErrorCode.TWO_FACTOR_REQUIRED
                ) {
                    AuthResult.NeedsSecondFactor
                } else {
                    AuthResult.Failed(outcome.error)
                }
        }
    }
}

/**
 * Registration is a distinct intent, not a fallback from a failed login: the
 * gateway refuses to create an account implicitly, which is what stops a typo'd
 * username from quietly becoming a new account.
 */
class RegisterUseCase @Inject constructor(private val repository: AuthRepository) {
    suspend operator fun invoke(username: String, password: String): AuthResult {
        CredentialRules.check(username, password)?.let { return AuthResult.Invalid(it) }
        return when (val outcome = repository.register(CredentialRules.normalize(username), password)) {
            is Outcome.Success -> AuthResult.Success(outcome.value)
            is Outcome.Failure -> AuthResult.Failed(outcome.error)
        }
    }
}

class LogoutUseCase @Inject constructor(private val repository: AuthRepository) {
    suspend operator fun invoke() = repository.logout()
}
