'use client'

import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'

import { useChatStore } from '@/entities/chat'
import { useSecretStore } from '@/entities/secret-chat'
import { useSessionStore } from '@/entities/session'
import { useUserDirectory } from '@/entities/user'
import { ProtocolError, useSyncAppClient, type Session } from '@/shared/api'
import { useTranslate, type TranslateFn } from '@/shared/i18n'
import { getDeviceId } from '@/shared/lib/id'

/** Maps a protocol failure onto a message the user can act on. */
function describeError(error: unknown, t: TranslateFn, registering: boolean): string {
  if (error instanceof ProtocolError) {
    switch (error.class) {
      case 'auth':
        return registering ? t('error.usernameTaken') : t('error.auth')
      case 'throttle':
        return t('error.rateLimited')
      case 'business':
        return registering ? t('error.usernameTaken') : t('error.auth')
      default:
        return error.message || t('error.unknown')
    }
  }
  if (error instanceof Error && /timed out|not connected|closed/i.test(error.message)) {
    return t('error.network')
  }
  return t('error.unknown')
}

export interface Credentials {
  username: string
  password: string
}

/** Sign-in and sign-up. Both are the same AUTH frame — `register` picks which. */
export function useAuthenticate() {
  const client = useSyncAppClient()
  const setSession = useSessionStore((state) => state.setSession)
  const loadChats = useChatStore((state) => state.load)
  const t = useTranslate()

  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const authenticate = useCallback(
    async ({ username, password }: Credentials, register: boolean) => {
      setPending(true)
      setError(null)
      try {
        client.setDeviceId(getDeviceId())
        const session: Session = await client.connect({
          kind: 'password',
          username: username.trim().replace(/^@/, ''),
          password,
          register,
        })
        setSession({
          userId: session.userId,
          // AUTH_OK now names the account, so the typed string is only a
          // fallback for a gateway too old to send one.
          username: session.username || username.trim().replace(/^@/, ''),
          deviceId: session.deviceId,
          sessionId: session.sessionId,
          token: session.token,
          resumeToken: session.resumeToken,
          displayName: session.displayName,
          avatarRef: session.avatarRef,
        })
        loadChats(session.userId)
        return true
      } catch (caught) {
        setError(describeError(caught, t, register))
        return false
      } finally {
        setPending(false)
      }
    },
    [client, setSession, loadChats, t],
  )

  return { authenticate, pending, error, clearError: () => setError(null) }
}

/**
 * Reconnects a stored session on load.
 *
 * "Auto login" here means re-authenticating with the bearer token from the last
 * AUTH_OK — the gateway accepts a token in place of credentials on any new
 * connection. A rejected token means the session is gone (expired or revoked),
 * so the stored session is dropped and the user lands on the login screen.
 */
export function useRestoreSession(): { restoring: boolean } {
  const client = useSyncAppClient()
  const status = useSessionStore((state) => state.status)
  const session = useSessionStore((state) => state.session)
  const hydrate = useSessionStore((state) => state.hydrate)
  const clear = useSessionStore((state) => state.clear)
  const updateSession = useSessionStore((state) => state.updateSession)
  const loadChats = useChatStore((state) => state.load)
  const [restoring, setRestoring] = useState(false)
  const attempted = useRef(false)
  /** bumped to re-run the effect after a failed attempt */
  const [attempt, setAttempt] = useState(0)
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    hydrate()
  }, [hydrate])

  useEffect(
    () => () => {
      if (retryTimer.current) clearTimeout(retryTimer.current)
    },
    [],
  )

  useEffect(() => {
    if (status !== 'authenticated' || !session?.token) return
    // Strict Mode mounts effects twice; a second dial would race the first, and
    // `attempted` is what prevents that. The state check skips the dial when one
    // is already in progress or established — but it has to admit 'closed' as
    // well as 'idle', because a failed first connect leaves the client exactly
    // there. Accepting only 'idle' made the retry below unreachable.
    const dialable = client.state === 'idle' || client.state === 'closed'
    if (attempted.current || !dialable) return
    attempted.current = true

    setRestoring(true)
    client.setDeviceId(session.deviceId || getDeviceId())
    loadChats(session.userId)
    client
      .connect({ kind: 'token', token: session.token })
      .then((fresh) => {
        // AUTH_OK carries the profile, so a name or avatar changed on another
        // device while this browser was closed is picked up on reconnect —
        // without it the stored copy would only ever be as new as the last
        // login. A gateway too old to send one leaves the stored copy alone.
        if (!fresh.username) return
        updateSession({
          username: fresh.username,
          displayName: fresh.displayName,
          avatarRef: fresh.avatarRef,
        })
      })
      .catch((error) => {
        if (error instanceof ProtocolError && error.class === 'auth') {
          clear()
          return
        }
        // Anything else is transient — the server was down, the network was not
        // up yet. The client deliberately does NOT retry a *first* connect on its
        // own (it rejects so the caller can report), and it clears its reconnect
        // flag when it does, so without a retry here the user sits on a fully
        // rendered app with a dead socket until they reload the page.
        retryTimer.current = setTimeout(
          () => {
            attempted.current = false
            setAttempt((n) => n + 1)
          },
          Math.min(1000 * 2 ** attempt, 30_000),
        )
      })
      .finally(() => setRestoring(false))
  }, [client, status, session, clear, loadChats, updateSession, attempt])

  return { restoring }
}

export function useLogout() {
  const client = useSyncAppClient()
  const clearSession = useSessionStore((state) => state.clear)
  const resetChats = useChatStore((state) => state.reset)
  const clearUsers = useUserDirectory((state) => state.clear)
  const forgetSecrets = useSecretStore((state) => state.forget)
  const queryClient = useQueryClient()

  return useCallback(() => {
    client.close()
    clearSession()
    resetChats()
    clearUsers()
    // Private keys and secret transcripts, from memory and from the vault. Two
    // reasons, and either alone would be enough: this may be a shared machine,
    // and the next account to sign in here would otherwise find the previous
    // one's identity still loaded and publish it as its own.
    forgetSecrets()
    // Cached history belongs to the account that just left.
    queryClient.clear()
  }, [client, clearSession, resetChats, clearUsers, forgetSecrets, queryClient])
}

/**
 * Watches for a session invalidated while we were connected (revoked from
 * another device, expired mid-use) and forces the app back to a clean state.
 */
export function useSessionExpiryWatcher(): void {
  const client = useSyncAppClient()
  const logout = useLogout()

  useEffect(() => client.on('sessionExpired', () => logout()), [client, logout])
}
