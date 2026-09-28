package com.syncapp.messenger.network.protocol

/**
 * Stable error codes carried in ERROR / AUTH_ERR bodies — mirrors
 * `server/pkg/wire/constants.go`. Codes are grouped by class in decimal ranges
 * so a client can react by range instead of enumerating every code.
 */
object ErrorCode {
    const val NONE = 0

    // 1xxx — transport / protocol
    const val PROTOCOL = 1000
    const val BAD_FRAME = 1001
    const val UNSUPPORTED = 1002
    const val PAYLOAD_TOO_BIG = 1003
    const val RESUME_EXPIRED = 1004

    // 2xxx — auth / session: the client must re-authenticate
    const val UNAUTHENTICATED = 2000
    const val BAD_TOKEN = 2001
    const val SESSION_REVOKED = 2002
    const val DEVICE_UNKNOWN = 2003

    /**
     * The password was right and a second factor is needed.
     *
     * In the AUTH band because it only ever arrives BEFORE a session exists, which is
     * exactly what [isAuth] means: "re-authenticate". A login screen shows a six-digit
     * field rather than reporting a bad password.
     */
    const val TWO_FACTOR_REQUIRED = 2004

    /**
     * A resume token that had already been rotated away was presented again, so two
     * parties held it and the session has been ended.
     *
     * Distinct from [RESUME_EXPIRED]: expiry is routine and recoverable with the bearer
     * token, while this means somebody else had the token — and it IS in the auth band,
     * because the session really is gone.
     */
    const val RESUME_REPLAYED = 2005

    // 3xxx — authorization / business: do not retry as-is
    const val FORBIDDEN = 3000
    const val NOT_FOUND = 3001
    const val CONFLICT = 3002
    const val BAD_ARG = 3003

    /**
     * A two-factor code did not verify.
     *
     * The BUSINESS band and not auth, deliberately: this also arrives on an
     * AUTHENTICATED connection — confirming an enrolment, disabling the factor — where
     * [isAuth] would discard the session and sign somebody out for mistyping six digits.
     */
    const val TWO_FACTOR_INVALID = 3004

    /**
     * The feature exists and this account's tier does not include it.
     *
     * Its own code rather than [FORBIDDEN], because the two lead somewhere different:
     * forbidden is final, this one has an answer — show the upgrade screen. Business band
     * for the same reason as above: it arrives on a live session.
     */
    const val PREMIUM_REQUIRED = 3005

    // 4xxx — throttling: retry after backoff, honour retryAfterMs
    const val RATE_LIMITED = 4000
    const val FLOOD = 4001

    // 5xxx — server: retry with backoff (writes are idempotent, so this is safe)
    const val INTERNAL = 5000
    const val UNAVAILABLE = 5001

    /** 2xxx means the session is gone; only a fresh login can fix it. */
    fun isAuth(code: Int): Boolean = code in 2000..2999

    /** Throttling and server-class failures are the ones a retry can resolve. */
    fun isRetryable(code: Int): Boolean = code in 4000..5999
}

/** An ERROR frame from the gateway, surfaced as an exception at the call site. */
class ProtocolException(
    val code: Int,
    override val message: String,
    val retryAfterMs: Int = 0,
) : Exception(message) {
    val isAuthFailure: Boolean get() = ErrorCode.isAuth(code)
    val isRetryable: Boolean get() = ErrorCode.isRetryable(code)
    override fun toString(): String = "ProtocolException(code=$code, message=$message)"
}
