'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { useSessionStore } from '@/entities/session'
import { MsgType, queryKeys, useIsConnected, useSyncAppClient, type Wire } from '@/shared/api'

/**
 * Where this account is signed in, and how to sign it out.
 *
 * Until the server grew these messages, "log out" was a purely local gesture:
 * the device dropped its token and the session stayed valid until it expired, so
 * a lost phone kept access for the rest of its lifetime. That is the gap this
 * feature closes, which is also why the revoke actions are deliberately blunt —
 * someone reaching for them has usually just lost a device.
 */
export interface ActiveSession {
  sessionId: string
  deviceId: string
  /** "web" | "android" | "ios" | "cli" | "" when the device row is gone */
  platform: string
  createdAt: number
  expiresAt: number
  /** true for the session this browser is using */
  current: boolean
}

export function useSessions() {
  const client = useSyncAppClient()
  const connected = useIsConnected()

  return useQuery({
    queryKey: queryKeys.sessions(),
    enabled: connected,
    // Short, because the list's whole job is to be trustworthy at the moment
    // someone is deciding whether to revoke something.
    staleTime: 10_000,
    queryFn: async (): Promise<ActiveSession[]> => {
      const reply = await client.request<Wire.Sessions>(
        MsgType.SESSION_LIST,
        {},
        {
          expect: MsgType.SESSIONS,
        },
      )
      return (
        reply.body.sessions
          .map((session) => ({
            sessionId: session.sessionId,
            deviceId: session.deviceId,
            platform: session.platform,
            createdAt: session.createdAt,
            expiresAt: session.expiresAt,
            current: session.current,
          }))
          // Newest first, but the current session always leads: it is the one the
          // reader is looking for to avoid, not to click.
          .sort((a, b) => Number(b.current) - Number(a.current) || b.createdAt - a.createdAt)
      )
    },
  })
}

/**
 * Revokes one session, or every other one.
 *
 * `sessionId: null` means "everywhere else" — the current connection survives,
 * because signing someone out of the device they are using to secure the account
 * is not what they asked for. Revoking the CURRENT session is allowed and is
 * simply a logout: the server closes the connection after answering, so the
 * caller must clear local state rather than wait for a reply that cannot come.
 */
export function useRevokeSession() {
  const client = useSyncAppClient()
  const queryClient = useQueryClient()
  const clearSession = useSessionStore((state) => state.clear)

  return useMutation({
    mutationFn: async (sessionId: string | null) => {
      const reply = await client.request<Wire.SessionRevoked>(
        MsgType.SESSION_REVOKE,
        { sessionId: sessionId ?? '', allIncludingCurrent: false },
        { expect: MsgType.SESSION_REVOKED },
      )
      return reply.body
    },
    onSuccess: (result) => {
      if (result.self) {
        // We just revoked ourselves. The socket is closing; drop the token now so
        // the app does not spend the next reconnect attempt presenting a dead one.
        client.close()
        clearSession()
        queryClient.clear()
        return
      }
      void queryClient.invalidateQueries({ queryKey: queryKeys.sessions() })
    },
  })
}

/**
 * Deletes the account after re-confirming the password.
 *
 * The password is asked for again even though the socket is authenticated: a
 * session token lives on the device, so without it anyone holding an unlocked
 * phone could destroy the account behind it. The server revokes every session
 * before answering, so there is nothing to clean up remotely — only locally.
 */
export function useDeleteAccount() {
  const client = useSyncAppClient()
  const queryClient = useQueryClient()
  const clearSession = useSessionStore((state) => state.clear)

  return useMutation({
    mutationFn: async ({ password, reason }: { password: string; reason?: string }) => {
      const reply = await client.request<Wire.AccountDeleted>(
        MsgType.ACCOUNT_DELETE,
        { password, reason: reason ?? '' },
        { expect: MsgType.ACCOUNT_DELETED },
      )
      return reply.body
    },
    onSuccess: () => {
      client.close()
      clearSession()
      // Everything cached belongs to an account that no longer exists. Secret-chat
      // material is device-local and survives this on purpose: it is keyed by
      // owner id and unreachable without a session, and wiping it here would also
      // wipe a different account's transcripts on a shared browser.
      queryClient.clear()
    },
  })
}
