package com.synapse.messenger.presentation.components

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.synapse.messenger.R
import com.synapse.messenger.core.AppError
import com.synapse.messenger.domain.usecase.CredentialProblem

/**
 * Turns a failure into something a person can read.
 *
 * Where the gateway supplied a message it wins: the server phrases business
 * failures better than a generic string can ("no such user: @bob", "too many new
 * chats"). Only the classes with no useful text of their own — offline, timeout —
 * fall back to our own wording.
 */
@Composable
fun AppError.localized(): String = when (this) {
    AppError.Offline -> stringResource(R.string.error_offline)
    AppError.Timeout -> stringResource(R.string.error_timeout)
    is AppError.Auth -> message ?: stringResource(R.string.error_auth)
    is AppError.NotFound -> message ?: stringResource(R.string.error_not_found)
    is AppError.Forbidden -> message ?: stringResource(R.string.error_forbidden)
    is AppError.RateLimited -> message ?: stringResource(R.string.error_rate_limited)
    is AppError.Rejected -> message ?: stringResource(R.string.error_generic)
    is AppError.Unexpected -> message ?: stringResource(R.string.error_generic)
}

@Composable
fun CredentialProblem.localized(): String = when (this) {
    CredentialProblem.USERNAME_TOO_SHORT -> stringResource(R.string.error_username_short)
    CredentialProblem.USERNAME_INVALID_CHARS -> stringResource(R.string.error_username_chars)
    CredentialProblem.PASSWORD_TOO_SHORT -> stringResource(R.string.error_password_short)
}
