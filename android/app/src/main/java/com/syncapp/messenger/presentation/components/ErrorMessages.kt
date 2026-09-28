package com.syncapp.messenger.presentation.components

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.syncapp.messenger.R
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.domain.usecase.CredentialProblem
import com.syncapp.messenger.network.protocol.ErrorCode

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
    /*
     * Three codes get their own text, and all three arrive on the same screens.
     *
     * The distinction is not cosmetic. PREMIUM_REQUIRED has an ANSWER — an upgrade —
     * while every other rejection is a dead end, and showing the server's own sentence
     * for it makes a purchasable feature look broken. A wrong second-factor code is worth
     * retrying and a demanded one means a field has not been filled in; both read as
     * "request rejected" through the generic branch.
     */
    is AppError.Rejected -> when (code) {
        ErrorCode.PREMIUM_REQUIRED -> stringResource(R.string.error_premium_required)
        ErrorCode.TWO_FACTOR_REQUIRED -> stringResource(R.string.error_two_factor_required)
        ErrorCode.TWO_FACTOR_INVALID -> stringResource(R.string.error_two_factor_invalid)
        else -> message ?: stringResource(R.string.error_generic)
    }
    is AppError.Unexpected -> message ?: stringResource(R.string.error_generic)
}

@Composable
fun CredentialProblem.localized(): String = when (this) {
    CredentialProblem.USERNAME_TOO_SHORT -> stringResource(R.string.error_username_short)
    CredentialProblem.USERNAME_INVALID_CHARS -> stringResource(R.string.error_username_chars)
    CredentialProblem.PASSWORD_TOO_SHORT -> stringResource(R.string.error_password_short)
}
