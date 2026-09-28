package com.syncapp.messenger.core

import com.syncapp.messenger.network.ConnectionClosedException
import com.syncapp.messenger.network.ConnectionState
import com.syncapp.messenger.network.NotConnectedException
import com.syncapp.messenger.network.RequestTimeoutException
import com.syncapp.messenger.network.protocol.ErrorCode
import com.syncapp.messenger.network.protocol.MsgType
import com.syncapp.messenger.network.protocol.ProtocolException
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/**
 * Every repository call goes through [runOutcome], so this mapping decides what
 * the user sees for every failure in the app. Two things matter and neither is
 * visible from a happy-path test: a class landing in the wrong bucket (an
 * expired session reported as "try again" leaves the user retrying a login that
 * can never succeed), and cancellation being swallowed.
 */
class OutcomeTest {

    @Test
    fun `wraps a successful result`() = runTest {
        val outcome = runOutcome { 42 }
        assertEquals(Outcome.Success(42), outcome)
    }

    @Test
    fun `getOrNull returns the value on success`() = runTest {
        assertEquals(42, runOutcome { 42 }.getOrNull())
    }

    @Test
    fun `getOrNull returns null on failure`() = runTest {
        val outcome = runOutcome<Int> { throw NotConnectedException(ConnectionState.CLOSED) }
        assertNull(outcome.getOrNull())
    }

    @Test
    fun `maps a dead connection to offline`() = runTest {
        val outcome = runOutcome<Unit> { throw NotConnectedException(ConnectionState.CLOSED) }
        assertEquals(Outcome.Failure(AppError.Offline), outcome)
    }

    @Test
    fun `maps a closed connection to offline`() = runTest {
        // A socket that dropped mid-request is the same situation as never having
        // had one, from the user's point of view: wait and retry.
        val outcome = runOutcome<Unit> { throw ConnectionClosedException() }
        assertEquals(Outcome.Failure(AppError.Offline), outcome)
    }

    @Test
    fun `maps a request timeout to timeout, not offline`() = runTest {
        // They are told apart because a timeout means the gateway is reachable but
        // slow — the request may well have been applied, so a blind retry is not
        // as safe as it is when the socket was never up.
        val outcome = runOutcome<Unit> { throw RequestTimeoutException(MsgType.SEND) }
        assertEquals(Outcome.Failure(AppError.Timeout), outcome)
    }

    @Test
    fun `maps an unrecognised exception to unexpected, keeping its message`() = runTest {
        val outcome = runOutcome<Unit> { throw IOException("disk full") }
        assertEquals(Outcome.Failure(AppError.Unexpected("disk full")), outcome)
    }

    @Test
    fun `survives an exception with no message`() = runTest {
        val outcome = runOutcome<Unit> { throw IllegalStateException() }
        assertEquals(Outcome.Failure(AppError.Unexpected(null)), outcome)
    }

    /**
     * Cancellation is control flow, not failure. A `catch (e: Exception)` that
     * swallows it breaks structured concurrency in a way nothing reports: the
     * coroutine keeps running after its scope was cancelled, and a ViewModel that
     * navigated away still writes to its own dead state.
     */
    @Test
    fun `rethrows cancellation instead of reporting it as a failure`() = runTest {
        try {
            runOutcome<Unit> { throw CancellationException("cancelled") }
            fail("cancellation must propagate, not be swallowed")
        } catch (expected: CancellationException) {
            assertEquals("cancelled", expected.message)
        }
    }

    @Test
    fun `lets a cancelled coroutine actually cancel`() = runTest {
        val job = async { runOutcome { delay(10_000); "never" } }
        job.cancel()

        assertTrue("the coroutine should be cancelled, not completed", job.isCancelled)
    }
}

/**
 * The protocol→AppError classification. Each branch decides an affordance: a
 * retry button, a trip back to the login screen, or a plain message.
 */
class ProtocolExceptionToAppErrorTest {

    private fun protocolError(code: Int, message: String = "nope", retryAfterMs: Int = 0) =
        ProtocolException(code, message, retryAfterMs)

    @Test
    fun `every auth code maps to Auth`() {
        // The whole 2xxx range means the session is gone. Classifying one of them
        // as Rejected would leave the user retrying a request that can never work.
        for (code in listOf(
            ErrorCode.UNAUTHENTICATED,
            ErrorCode.BAD_TOKEN,
            ErrorCode.SESSION_REVOKED,
            ErrorCode.DEVICE_UNKNOWN,
        )) {
            val error = protocolError(code).toAppError()
            assertTrue("code $code should be Auth but was $error", error is AppError.Auth)
        }
    }

    @Test
    fun `an unknown code in the auth range still maps to Auth`() {
        // The range check exists precisely so a newer server can add a code this
        // build has never heard of without it being misclassified.
        assertTrue(protocolError(2999).toAppError() is AppError.Auth)
    }

    @Test
    fun `not found maps to NotFound`() {
        assertEquals(AppError.NotFound("no such user"), protocolError(ErrorCode.NOT_FOUND, "no such user").toAppError())
    }

    @Test
    fun `forbidden maps to Forbidden`() {
        assertEquals(AppError.Forbidden("not a member"), protocolError(ErrorCode.FORBIDDEN, "not a member").toAppError())
    }

    @Test
    fun `rate limiting carries the retry hint`() {
        // Dropping retryAfterMs would make the client retry immediately and get
        // throttled again — a loop the user cannot get out of.
        val error = protocolError(ErrorCode.RATE_LIMITED, "slow down", retryAfterMs = 2500).toAppError()
        assertEquals(AppError.RateLimited(2500, "slow down"), error)
    }

    @Test
    fun `flood maps to RateLimited too`() {
        val error = protocolError(ErrorCode.FLOOD, "flooding", retryAfterMs = 5000).toAppError()
        assertEquals(AppError.RateLimited(5000, "flooding"), error)
    }

    @Test
    fun `an unclassified code falls through to Rejected with its code intact`() {
        // The code is what a bug report needs; a generic "something went wrong"
        // loses the only diagnosable detail.
        val error = protocolError(ErrorCode.CONFLICT, "already exists").toAppError()
        assertEquals(AppError.Rejected(ErrorCode.CONFLICT, "already exists"), error)
    }

    @Test
    fun `server-class codes are Rejected, not Auth`() {
        for (code in listOf(ErrorCode.INTERNAL, ErrorCode.UNAVAILABLE)) {
            val error = protocolError(code).toAppError()
            assertTrue("code $code should be Rejected but was $error", error is AppError.Rejected)
        }
    }

    @Test
    fun `the gateway message is preserved, because it phrases things better`() {
        // "no such user: @bob" beats any string this client could compose.
        val error = protocolError(ErrorCode.NOT_FOUND, "no such user: @bob").toAppError()
        assertEquals("no such user: @bob", error.message)
    }

    @Test
    fun `runOutcome routes a protocol exception through the same classification`() = runTest {
        val outcome = runOutcome<Unit> {
            throw protocolError(ErrorCode.RATE_LIMITED, "slow down", retryAfterMs = 1000)
        }
        assertEquals(Outcome.Failure(AppError.RateLimited(1000, "slow down")), outcome)
    }
}

/** The range predicates every other classification is built on. */
class ErrorCodeTest {

    @Test
    fun `auth is exactly the 2xxx range`() {
        assertTrue(ErrorCode.isAuth(2000))
        assertTrue(ErrorCode.isAuth(2999))
        assertTrue(!ErrorCode.isAuth(1999))
        assertTrue(!ErrorCode.isAuth(3000))
    }

    @Test
    fun `every declared auth code is in the auth range`() {
        for (code in listOf(
            ErrorCode.UNAUTHENTICATED,
            ErrorCode.BAD_TOKEN,
            ErrorCode.SESSION_REVOKED,
            ErrorCode.DEVICE_UNKNOWN,
        )) {
            assertTrue("$code should be auth", ErrorCode.isAuth(code))
        }
    }

    @Test
    fun `retryable covers throttling and server failures`() {
        // 4xxx is "you went too fast" and 5xxx is "we broke"; both resolve on
        // their own. 3xxx never does — retrying a FORBIDDEN is pure noise.
        assertTrue(ErrorCode.isRetryable(ErrorCode.RATE_LIMITED))
        assertTrue(ErrorCode.isRetryable(ErrorCode.FLOOD))
        assertTrue(ErrorCode.isRetryable(ErrorCode.INTERNAL))
        assertTrue(ErrorCode.isRetryable(ErrorCode.UNAVAILABLE))
    }

    @Test
    fun `business and auth failures are not retryable`() {
        for (code in listOf(
            ErrorCode.FORBIDDEN,
            ErrorCode.NOT_FOUND,
            ErrorCode.CONFLICT,
            ErrorCode.BAD_ARG,
            ErrorCode.UNAUTHENTICATED,
            ErrorCode.BAD_TOKEN,
        )) {
            assertTrue("$code must not be retryable", !ErrorCode.isRetryable(code))
        }
    }

    @Test
    fun `transport failures are not retryable as-is`() {
        // A BAD_FRAME or an UNSUPPORTED type means this client and this server
        // disagree; sending the same bytes again produces the same rejection.
        assertTrue(!ErrorCode.isRetryable(ErrorCode.BAD_FRAME))
        assertTrue(!ErrorCode.isRetryable(ErrorCode.UNSUPPORTED))
    }

    @Test
    fun `no code is both auth and retryable`() {
        // The two predicates drive mutually exclusive affordances — re-login
        // versus retry — so an overlap would show both at once.
        for (code in 0..6000) {
            assertTrue("$code is both", !(ErrorCode.isAuth(code) && ErrorCode.isRetryable(code)))
        }
    }

    @Test
    fun `codes are allocated once each`() {
        val codes = listOf(
            ErrorCode.NONE, ErrorCode.PROTOCOL, ErrorCode.BAD_FRAME, ErrorCode.UNSUPPORTED,
            ErrorCode.PAYLOAD_TOO_BIG, ErrorCode.RESUME_EXPIRED, ErrorCode.UNAUTHENTICATED,
            ErrorCode.BAD_TOKEN, ErrorCode.SESSION_REVOKED, ErrorCode.DEVICE_UNKNOWN,
            ErrorCode.FORBIDDEN, ErrorCode.NOT_FOUND, ErrorCode.CONFLICT, ErrorCode.BAD_ARG,
            ErrorCode.RATE_LIMITED, ErrorCode.FLOOD, ErrorCode.INTERNAL, ErrorCode.UNAVAILABLE,
        )
        assertEquals("duplicate error code", codes.size, codes.toSet().size)
    }
}

/** The exception itself — its predicates gate the reconnect path. */
class ProtocolExceptionTest {

    @Test
    fun `reports an auth failure for a 2xxx code`() {
        assertTrue(ProtocolException(ErrorCode.BAD_TOKEN, "expired").isAuthFailure)
    }

    @Test
    fun `does not report an auth failure for a business code`() {
        assertTrue(!ProtocolException(ErrorCode.NOT_FOUND, "gone").isAuthFailure)
    }

    @Test
    fun `reports retryable for a throttle code`() {
        assertTrue(ProtocolException(ErrorCode.RATE_LIMITED, "slow").isRetryable)
    }

    @Test
    fun `defaults the retry hint to zero`() {
        assertEquals(0, ProtocolException(ErrorCode.INTERNAL, "boom").retryAfterMs)
    }

    @Test
    fun `carries its message as the exception message`() {
        // It is surfaced verbatim in the UI, so it has to survive being thrown.
        val exception = ProtocolException(ErrorCode.NOT_FOUND, "no such user: @bob")
        assertEquals("no such user: @bob", exception.message)
    }

    @Test
    fun `names its code in toString for logs`() {
        val text = ProtocolException(ErrorCode.FORBIDDEN, "nope").toString()
        assertTrue("toString should name the code: $text", text.contains("3000"))
    }

    @Test
    fun `is an Exception, so runOutcome can catch it`() {
        val thrown: Exception = ProtocolException(ErrorCode.INTERNAL, "boom")
        assertSame(ProtocolException::class.java, thrown.javaClass)
    }
}
