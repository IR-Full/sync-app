package com.synapse.messenger.core

import com.synapse.messenger.network.ConnectionClosedException
import com.synapse.messenger.network.NotConnectedException
import com.synapse.messenger.network.RequestTimeoutException
import com.synapse.messenger.network.protocol.ErrorCode
import com.synapse.messenger.network.protocol.ProtocolException
import kotlinx.coroutines.CancellationException

/**
 * The result of an operation that talks to the gateway.
 *
 * Errors are explicit rather than thrown across layers: a repository returns
 * [Outcome.Failure] with a classified [AppError], and the ViewModel decides what
 * the user sees. Cancellation is never swallowed — it is a control-flow signal,
 * not a failure.
 */
sealed interface Outcome<out T> {
    data class Success<T>(val value: T) : Outcome<T>

    data class Failure(val error: AppError) : Outcome<Nothing>

    fun getOrNull(): T? = (this as? Success)?.value
}

/**
 * A failure in terms the UI can act on. [Offline] and [Timeout] are worth a
 * retry button; [Auth] means the session is gone; [Rejected] carries the
 * gateway's own message because the server phrases business errors better than a
 * generic string could ("no such user: @bob").
 */
sealed interface AppError {
    val message: String?

    data object Offline : AppError {
        override val message: String? = null
    }

    data object Timeout : AppError {
        override val message: String? = null
    }

    data class Auth(override val message: String?) : AppError

    data class NotFound(override val message: String?) : AppError

    data class Forbidden(override val message: String?) : AppError

    data class RateLimited(val retryAfterMs: Int, override val message: String?) : AppError

    data class Rejected(val code: Int, override val message: String?) : AppError

    data class Unexpected(override val message: String?) : AppError
}

/** Runs [block], mapping protocol and transport failures onto [AppError]. */
suspend inline fun <T> runOutcome(crossinline block: suspend () -> T): Outcome<T> = try {
    Outcome.Success(block())
} catch (e: CancellationException) {
    throw e
} catch (e: ProtocolException) {
    Outcome.Failure(e.toAppError())
} catch (e: NotConnectedException) {
    Outcome.Failure(AppError.Offline)
} catch (e: ConnectionClosedException) {
    Outcome.Failure(AppError.Offline)
} catch (e: RequestTimeoutException) {
    Outcome.Failure(AppError.Timeout)
} catch (e: Exception) {
    Outcome.Failure(AppError.Unexpected(e.message))
}

fun ProtocolException.toAppError(): AppError = when {
    isAuthFailure -> AppError.Auth(message)
    code == ErrorCode.NOT_FOUND -> AppError.NotFound(message)
    code == ErrorCode.FORBIDDEN -> AppError.Forbidden(message)
    code == ErrorCode.RATE_LIMITED || code == ErrorCode.FLOOD ->
        AppError.RateLimited(retryAfterMs, message)
    else -> AppError.Rejected(code, message)
}
