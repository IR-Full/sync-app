'use client'

import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'

import { useChatStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { useUserDirectory } from '@/entities/user'
import { ProtocolError, useSynapseClient, type Session } from '@/shared/api'
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
  const client = useSynapseClient()
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
          username: username.trim().replace(/^@/, ''),
          deviceId: session.deviceId,
          sessionId: session.sessionId,
          token: session.token,
          resumeToken: session.resumeToken,
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
  const client = useSynapseClient()
  const status = useSessionStore((state) => state.status)
  const session = useSessionStore((state) => state.session)
  const hydrate = useSessionStore((state) => state.hydrate)
  const clear = useSessionStore((state) => state.clear)
  const loadChats = useChatStore((state) => state.load)
  const [restoring, setRestoring] = useState(false)
  const attempted = useRef(false)

  useEffect(() => {
    hydrate()
  }, [hydrate])

  useEffect(() => {
    if (status !== 'authenticated' || !session?.token) return
    // Strict Mode mounts effects twice; a second dial would race the first.
    if (attempted.current || client.state !== 'idle') return
    attempted.current = true

    setRestoring(true)
    client.setDeviceId(session.deviceId || getDeviceId())
    loadChats(session.userId)
    client
      .connect({ kind: 'token', token: session.token })
      .catch((error) => {
        if (error instanceof ProtocolError && error.class === 'auth') clear()
      })
      .finally(() => setRestoring(false))
  }, [client, status, session, clear, loadChats])

  return { restoring }
}

export function useLogout() {
  const client = useSynapseClient()
  const clearSession = useSessionStore((state) => state.clear)
  const resetChats = useChatStore((state) => state.reset)
  const clearUsers = useUserDirectory((state) => state.clear)
  const queryClient = useQueryClient()

  return useCallback(() => {
    client.close()
    clearSession()
    resetChats()
    clearUsers()
    // Cached history belongs to the account that just left.
    queryClient.clear()
  }, [client, clearSession, resetChats, clearUsers, queryClient])
}

/**
 * Watches for a session invalidated while we were connected (revoked from
 * another device, expired mid-use) and forces the app back to a clean state.
 */
export function useSessionExpiryWatcher(): void {
  const client = useSynapseClient()
  const logout = useLogout()

  useEffect(() => client.on('sessionExpired', () => logout()), [client, logout])
}
