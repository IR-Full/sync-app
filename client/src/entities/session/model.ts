'use client'

import { create } from 'zustand'

import { readStorage, removeStorage, StorageKeys, writeStorage } from '@/shared/lib/storage'

export interface StoredSession {
  userId: string
  username: string
  deviceId: string
  sessionId: string
  token: string
  resumeToken: string
  /** local-only display name; the protocol has no profile-update message */
  displayName?: string
}

export type AuthStatus = 'unknown' | 'anonymous' | 'authenticated'

interface SessionState {
  status: AuthStatus
  session: StoredSession | null
  setSession: (session: StoredSession) => void
  updateSession: (patch: Partial<StoredSession>) => void
  clear: () => void
  hydrate: () => void
}

/**
 * The authenticated session.
 *
 * On token storage: the gateway hands the bearer token back inside an AUTH_OK
 * frame on the WebSocket. There is no HTTP auth endpoint anywhere in the server
 * (`cmd/server/main.go` mounts only /ws, /healthz, /metrics and the media
 * routes), so no server-set httpOnly cookie is possible — the token necessarily
 * reaches JavaScript, and localStorage is where it can live. Documented in the
 * README as a known trade-off rather than hidden.
 *
 * Zustand, not TanStack Query: this is client-owned state with no server
 * endpoint to fetch or revalidate against.
 */
export const useSessionStore = create<SessionState>((set, get) => ({
  status: 'unknown',
  session: null,

  setSession: (session) => {
    writeStorage(StorageKeys.session, session)
    set({ session, status: 'authenticated' })
  },

  updateSession: (patch) => {
    const current = get().session
    if (!current) return
    const next = { ...current, ...patch }
    writeStorage(StorageKeys.session, next)
    set({ session: next })
  },

  clear: () => {
    removeStorage(StorageKeys.session)
    set({ session: null, status: 'anonymous' })
  },

  hydrate: () => {
    const stored = readStorage<StoredSession | null>(StorageKeys.session, null)
    if (stored?.token && stored.userId) {
      set({ session: stored, status: 'authenticated' })
    } else {
      set({ session: null, status: 'anonymous' })
    }
  },
}))

export function useCurrentUserId(): string | null {
  return useSessionStore((state) => state.session?.userId ?? null)
}
