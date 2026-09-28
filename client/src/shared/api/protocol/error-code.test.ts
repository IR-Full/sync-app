import { describe, expect, it } from 'vitest'

import { ErrorCode, ProtocolError, errorClass, isAuthError, isRetryable } from './error-code'

/**
 * The ranges are the contract. A client reacts by CLASS so it can handle a code
 * the server added after this build shipped — which only works if an unknown
 * code inside a known range still classifies.
 */
describe('errorClass', () => {
  it('classifies each documented code', () => {
    expect(errorClass(ErrorCode.BAD_FRAME)).toBe('transport')
    expect(errorClass(ErrorCode.RESUME_EXPIRED)).toBe('transport')
    expect(errorClass(ErrorCode.UNAUTHENTICATED)).toBe('auth')
    expect(errorClass(ErrorCode.SESSION_REVOKED)).toBe('auth')
    expect(errorClass(ErrorCode.FORBIDDEN)).toBe('business')
    expect(errorClass(ErrorCode.BAD_ARG)).toBe('business')
    expect(errorClass(ErrorCode.RATE_LIMITED)).toBe('throttle')
    expect(errorClass(ErrorCode.FLOOD)).toBe('throttle')
    expect(errorClass(ErrorCode.INTERNAL)).toBe('server')
    expect(errorClass(ErrorCode.UNAVAILABLE)).toBe('server')
  })

  it('classifies codes this build has never seen', () => {
    // The whole reason for ranges: a gateway newer than this bundle can add a
    // code and the client still reacts sensibly.
    expect(errorClass(1999)).toBe('transport')
    expect(errorClass(2999)).toBe('auth')
    expect(errorClass(3999)).toBe('business')
    expect(errorClass(4999)).toBe('throttle')
    expect(errorClass(5999)).toBe('server')
  })

  it('reports anything outside the ranges as unknown', () => {
    for (const code of [0, 1, 999, 6000, 99999, -1]) {
      expect(errorClass(code)).toBe('unknown')
    }
  })

  it('places each range boundary on the correct side', () => {
    expect(errorClass(1000)).toBe('transport')
    expect(errorClass(2000)).toBe('auth')
    expect(errorClass(3000)).toBe('business')
    expect(errorClass(4000)).toBe('throttle')
    expect(errorClass(5000)).toBe('server')
  })
})

describe('isAuthError', () => {
  it('is true only for the auth range', () => {
    // This is what decides whether the stored session is wiped and the user is
    // sent back to the login screen, so a false positive logs someone out over a
    // transient failure.
    expect(isAuthError(ErrorCode.UNAUTHENTICATED)).toBe(true)
    expect(isAuthError(ErrorCode.BAD_TOKEN)).toBe(true)
    expect(isAuthError(ErrorCode.SESSION_REVOKED)).toBe(true)

    expect(isAuthError(ErrorCode.INTERNAL)).toBe(false)
    expect(isAuthError(ErrorCode.RATE_LIMITED)).toBe(false)
    expect(isAuthError(ErrorCode.FORBIDDEN)).toBe(false)
  })

  it('does not treat an expired resume as a dead session', () => {
    // RESUME_EXPIRED is deliberately in the transport range: it happens on a
    // routine reconnect, and logging the user out for it would be a bug the
    // user experiences as being signed out at random.
    expect(isAuthError(ErrorCode.RESUME_EXPIRED)).toBe(false)
    expect(errorClass(ErrorCode.RESUME_EXPIRED)).toBe('transport')
  })
})

describe('isRetryable', () => {
  it('allows a retry for transient classes', () => {
    expect(isRetryable(ErrorCode.INTERNAL)).toBe(true)
    expect(isRetryable(ErrorCode.UNAVAILABLE)).toBe(true)
    expect(isRetryable(ErrorCode.RATE_LIMITED)).toBe(true)
    expect(isRetryable(ErrorCode.FLOOD)).toBe(true)
    expect(isRetryable(ErrorCode.BAD_FRAME)).toBe(true)
  })

  it('refuses a retry for failures a retry cannot fix', () => {
    // Re-sending a forbidden or malformed request just spends the flood budget.
    expect(isRetryable(ErrorCode.FORBIDDEN)).toBe(false)
    expect(isRetryable(ErrorCode.BAD_ARG)).toBe(false)
    expect(isRetryable(ErrorCode.NOT_FOUND)).toBe(false)
    expect(isRetryable(ErrorCode.UNAUTHENTICATED)).toBe(false)
  })
})

describe('ProtocolError', () => {
  it('carries the code, message and retry hint', () => {
    const err = new ProtocolError(ErrorCode.RATE_LIMITED, 'slow down', 1500)
    expect(err.code).toBe(ErrorCode.RATE_LIMITED)
    expect(err.message).toBe('slow down')
    expect(err.retryAfterMs).toBe(1500)
    expect(err.name).toBe('ProtocolError')
    expect(err).toBeInstanceOf(Error)
  })

  it('exposes its class so callers can branch without knowing the code', () => {
    expect(new ProtocolError(ErrorCode.SESSION_REVOKED, '').class).toBe('auth')
    expect(new ProtocolError(ErrorCode.FLOOD, '').class).toBe('throttle')
  })

  it('falls back to a readable message when the server sends none', () => {
    const err = new ProtocolError(ErrorCode.INTERNAL, '')
    expect(err.message).toContain('5000')
  })

  it('defaults retryAfterMs to zero', () => {
    expect(new ProtocolError(ErrorCode.FORBIDDEN, 'no').retryAfterMs).toBe(0)
  })
})
