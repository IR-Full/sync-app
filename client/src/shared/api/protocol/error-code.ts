/**
 * Protocol error codes — mirrors `server/pkg/wire/constants.go`.
 *
 * Codes are grouped into decimal ranges so a client can react by CLASS without
 * enumerating every code: 1xxx transport, 2xxx auth, 3xxx business, 4xxx
 * throttle, 5xxx server. Unknown codes still land in a known class.
 */
export const ErrorCode = {
  NONE: 0,

  // 1xxx — transport / protocol
  PROTOCOL: 1000,
  BAD_FRAME: 1001,
  UNSUPPORTED: 1002,
  PAYLOAD_TOO_BIG: 1003,
  RESUME_EXPIRED: 1004,

  // 2xxx — auth / session (client must re-authenticate)
  UNAUTHENTICATED: 2000,
  BAD_TOKEN: 2001,
  SESSION_REVOKED: 2002,
  DEVICE_UNKNOWN: 2003,

  // 3xxx — authorization / business (do not retry as-is)
  FORBIDDEN: 3000,
  NOT_FOUND: 3001,
  CONFLICT: 3002,
  BAD_ARG: 3003,

  // 4xxx — throttling (retry after backoff, honour retryAfterMs)
  RATE_LIMITED: 4000,
  FLOOD: 4001,

  // 5xxx — server (retry with backoff; safe because writes are idempotent)
  INTERNAL: 5000,
  UNAVAILABLE: 5001,
} as const

export type ErrorCode = (typeof ErrorCode)[keyof typeof ErrorCode]

export type ErrorClass = 'transport' | 'auth' | 'business' | 'throttle' | 'server' | 'unknown'

export function errorClass(code: number): ErrorClass {
  if (code >= 1000 && code < 2000) return 'transport'
  if (code >= 2000 && code < 3000) return 'auth'
  if (code >= 3000 && code < 4000) return 'business'
  if (code >= 4000 && code < 5000) return 'throttle'
  if (code >= 5000 && code < 6000) return 'server'
  return 'unknown'
}

/** Auth-class errors invalidate the stored session: the client must log in again. */
export function isAuthError(code: number): boolean {
  return errorClass(code) === 'auth'
}

/** Transport/throttle/server classes are transient — the same request may succeed later. */
export function isRetryable(code: number): boolean {
  const cls = errorClass(code)
  return cls === 'throttle' || cls === 'server' || cls === 'transport'
}

/** An error returned by the gateway in a MsgError / MsgAuthErr body. */
export class ProtocolError extends Error {
  readonly code: number
  readonly retryAfterMs: number

  constructor(code: number, message: string, retryAfterMs = 0) {
    super(message || `protocol error ${code}`)
    this.name = 'ProtocolError'
    this.code = code
    this.retryAfterMs = retryAfterMs
  }

  get class(): ErrorClass {
    return errorClass(this.code)
  }

  get retryable(): boolean {
    return isRetryable(this.code)
  }
}
