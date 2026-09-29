'use client'

import { useCallback, useEffect, useState } from 'react'

import { ErrorCode, MsgType, ProtocolError, useSyncAppClient, type Wire } from '@/shared/api'

/**
 * Changing a password, and the second factor.
 *
 * Neither was possible. The password could not be changed by any path, so a leaked
 * one meant a permanently lost account — revoking every session does not stop whoever
 * knows the password from signing in again a minute later. And there was no second
 * factor at all, which is the other half of the same gap: one interceptable
 * credential and nothing behind it.
 */

/** The second-factor state a settings screen draws. */
export interface TwoFactorState {
  enabled: boolean
  /**
   * How many unspent recovery codes remain.
   *
   * Shown because losing the last code and the phone together is the state there is
   * no way back from, and a screen that cannot see the count cannot warn before it
   * happens.
   */
  recoveryLeft: number
}

/** What TOTP enrolment returns for the user to scan or type. */
export interface TOTPSetup {
  /** the base32 secret, for typing in by hand */
  secret: string
  /** the otpauth:// URI, for a QR code */
  uri: string
}

export function useAccountSecurity(): {
  twoFactor: TwoFactorState | null
  refresh: () => Promise<void>
  changePassword: (oldPassword: string, newPassword: string) => Promise<number>
  beginTOTP: () => Promise<TOTPSetup>
  confirmTOTP: (code: string) => Promise<string[]>
  disableTOTP: (password: string, code: string) => Promise<void>
} {
  const client = useSyncAppClient()
  const [twoFactor, setTwoFactor] = useState<TwoFactorState | null>(null)

  const applyState = useCallback((body: Wire.TOTPState) => {
    setTwoFactor({ enabled: body.enabled ?? false, recoveryLeft: body.recoveryLeft ?? 0 })
  }, [])

  /**
   * Reads the current state.
   *
   * TOTP_STATE is now answerable as a QUESTION, not only as the reply to a write. It
   * used to be reply-only, and this function was a no-op that relied on some earlier
   * write having populated the state — so a settings screen opened on a fresh page load
   * drew "two-factor: off" no matter what was actually configured. That is the worst
   * available default for a security toggle: it invites the user to enrol a second time
   * and reports the resulting refusal as an error.
   *
   * An older gateway does not dispatch the type and answers UNSUPPORTED. That is
   * reported as "unknown" rather than "off", because those are different claims.
   */
  const refresh = useCallback(async () => {
    try {
      const reply = await client.request<Wire.TOTPState>(
        MsgType.TOTP_STATE,
        {},
        { expect: MsgType.TOTP_STATE },
      )
      applyState(reply.body)
    } catch (error) {
      if (error instanceof ProtocolError && error.code === ErrorCode.UNSUPPORTED) {
        setTwoFactor(null)
        return
      }
      throw error
    }
  }, [applyState, client])

  const changePassword = useCallback(
    async (oldPassword: string, newPassword: string) => {
      const reply = await client.request<Wire.PasswordChanged>(
        MsgType.PASSWORD_CHANGE,
        { oldPassword, newPassword },
        { expect: MsgType.PASSWORD_CHANGED },
      )
      // How many OTHER sessions were signed out. Worth surfacing rather than
      // swallowing: a password change is usually a response to suspecting someone else
      // has access, and "four other devices were signed out" is the confirmation the
      // user is actually looking for.
      return reply.body.sessionsRevoked ?? 0
    },
    [client],
  )

  const beginTOTP = useCallback(async () => {
    const reply = await client.request<Wire.TOTPSetupInfo>(
      MsgType.TOTP_SETUP,
      {},
      { expect: MsgType.TOTP_SETUP_INFO },
    )
    return { secret: reply.body.secret, uri: reply.body.uri }
  }, [client])

  const confirmTOTP = useCallback(
    async (code: string) => {
      const reply = await client.request<Wire.TOTPState>(
        MsgType.TOTP_CONFIRM,
        { code },
        { expect: MsgType.TOTP_STATE },
      )
      applyState(reply.body)
      /*
       * The recovery codes, returned exactly once.
       *
       * The server stores argon2id hashes, so there is nothing to show later — which
       * is what makes a leak of that table worthless, and why the caller has to put
       * these in front of the user NOW. A screen that logs them and moves on has
       * quietly removed the account's only recovery path.
       */
      return reply.body.recoveryCodes ?? []
    },
    [applyState, client],
  )

  const disableTOTP = useCallback(
    async (password: string, code: string) => {
      const reply = await client.request<Wire.TOTPState>(
        MsgType.TOTP_DISABLE,
        { password, code },
        { expect: MsgType.TOTP_STATE },
      )
      applyState(reply.body)
    },
    [applyState, client],
  )

  return { twoFactor, refresh, changePassword, beginTOTP, confirmTOTP, disableTOTP }
}

/**
 * Turns a protocol error into something a form can say.
 *
 * The distinctions matter more here than elsewhere, because the same screen shows
 * several failures that look alike and mean different things:
 *
 *   - TWO_FACTOR_INVALID is a mistyped code. It is in the BUSINESS band, not auth, so
 *     the client must NOT treat it as a dead session — the connection is fine and the
 *     user is six digits away from succeeding.
 *   - FORBIDDEN on these paths means the PASSWORD was wrong, which is a different
 *     field to highlight.
 *   - BAD_ARG is a rule the new password broke.
 */
export function describeSecurityError(
  error: unknown,
): 'code' | 'password' | 'weak' | 'unknown' {
  if (!(error instanceof ProtocolError)) return 'unknown'
  switch (error.code) {
    case ErrorCode.TWO_FACTOR_INVALID:
      return 'code'
    case ErrorCode.FORBIDDEN:
      return 'password'
    case ErrorCode.BAD_ARG:
      return 'weak'
    default:
      return 'unknown'
  }
}

/**
 * Watches for the login-time two-factor challenge.
 *
 * ErrorCode.TWO_FACTOR_REQUIRED means the password was RIGHT and a code is needed, so
 * the login screen has to show a six-digit prompt rather than "wrong password". It is
 * in the auth band — correctly, since no session exists yet — which is why a client
 * that treated every auth-class error as "credentials rejected" would tell the user
 * their password was wrong when it was not.
 */
export function useTwoFactorChallenge(): { required: boolean; clear: () => void } {
  const client = useSyncAppClient()
  const [required, setRequired] = useState(false)

  useEffect(() => {
    return client.on('error', (error) => {
      if (error.code === ErrorCode.TWO_FACTOR_REQUIRED) setRequired(true)
    })
  }, [client])

  return { required, clear: useCallback(() => setRequired(false), []) }
}
