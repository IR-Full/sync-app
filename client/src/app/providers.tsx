'use client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'

import { useChatStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { useSettingsStore } from '@/entities/settings'
import { useRestoreSession, useSessionExpiryWatcher } from '@/features/auth'
import { CallOverlay, useCallEngine } from '@/features/calls'
import { useDraftSync } from '@/features/drafts'
import { useMessageNotifications } from '@/features/notifications'
import { useReceiptReset } from '@/features/read-receipts'
import { useChatListSync } from '@/features/chat-list-sync'
import { useRealtimeSync } from '@/features/realtime-sync'
import { useSubscriptionSync } from '@/features/billing'
import {
  useSecretChatEngine,
  useSecretKeyPublisher,
  useSecretSync,
} from '@/features/secret-chats'
import { useOutboxFlush, useOutboxStore } from '@/features/send-message'
import { ProtocolError, SyncAppProvider, useIsConnected } from '@/shared/api'
import { useLocaleStore } from '@/shared/i18n'
import { useThemeStore } from '@/shared/theme/model'

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Data arrives over a push socket, so the usual polling-oriented
        // defaults are wrong here: nothing goes stale on a timer, and a window
        // focus does not mean the cache is behind.
        staleTime: 30_000,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
        retry: (failureCount, error) => {
          // Business and auth failures will fail identically on a retry.
          if (error instanceof ProtocolError && !error.retryable) return false
          return failureCount < 2
        },
      },
      mutations: { retry: false },
    },
  })
}

/**
 * Runs the app-wide subscriptions exactly once.
 *
 * These are all singletons by nature — one socket bridge, one outbox drain, one
 * notification listener. Mounting them here instead of inside screens means
 * navigating between chats never tears down and re-arms the realtime plumbing.
 */
function AppBootstrap({ children }: { children: ReactNode }) {
  const hydrateTheme = useThemeStore((state) => state.hydrate)
  const hydrateLocale = useLocaleStore((state) => state.hydrate)
  const hydrateSettings = useSettingsStore((state) => state.hydrate)
  const userId = useSessionStore((state) => state.session?.userId)
  const loadOutbox = useOutboxStore((state) => state.load)
  const loadChats = useChatStore((state) => state.load)
  const connected = useIsConnected()

  useEffect(() => hydrateTheme(), [hydrateTheme])
  useEffect(() => hydrateLocale(), [hydrateLocale])
  useEffect(() => hydrateSettings(), [hydrateSettings])

  // Chats and the outbox are per-account stores; load them when we know who is
  // signed in, and reload if the account changes.
  useEffect(() => {
    if (!userId) return
    loadChats(userId)
    loadOutbox(userId)
  }, [userId, loadChats, loadOutbox])

  useRestoreSession()
  useSessionExpiryWatcher()
  useRealtimeSync()
  // Enumerates the account's chats on connect. Without it a browser that has
  // never received traffic for a chat — a fresh profile, cleared storage, a
  // second browser — opens to an empty list it has no way to populate.
  useChatListSync()
  useCallEngine()
  useSecretKeyPublisher()
  useSecretChatEngine()
  // AFTER the engine: the sync asks the server to replay what this device missed,
  // and the replayed frames arrive as ordinary SECRET_RECV — so the handler that
  // decrypts them has to be listening before any of them can land.
  useSecretSync()
  // Entitlements, kept current by a request on connect AND by a push: a
  // subscription changes without this client asking, and a client showing the old
  // tier offers features the server has started refusing.
  useSubscriptionSync()
  useDraftSync()
  useOutboxFlush()
  useMessageNotifications()
  useReceiptReset(connected)

  return (
    <>
      {children}
      {/* A call outlives navigation, so its surface lives above the routes. */}
      <CallOverlay />
    </>
  )
}

export function Providers({ children }: { children: ReactNode }) {
  // Created in state so a Fast Refresh does not discard the cache.
  const [queryClient] = useState(createQueryClient)

  return (
    <QueryClientProvider client={queryClient}>
      <SyncAppProvider>
        <AppBootstrap>{children}</AppBootstrap>
      </SyncAppProvider>
    </QueryClientProvider>
  )
}
