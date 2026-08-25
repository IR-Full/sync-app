'use client'

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react'

import { config } from '../../config/env'
import { getDeviceId } from '../../lib/id'
import { SyncAppClient, type ConnectionState } from '../protocol'

interface SyncAppContextValue {
  client: SyncAppClient
  state: ConnectionState
  /** browser-level connectivity, which is a different question from "is the socket up" */
  online: boolean
}

const SyncAppContext = createContext<SyncAppContextValue | null>(null)

/** Browser connectivity as an external store, so it needs no mirroring state. */
function subscribeToConnectivity(onChange: () => void): () => void {
  window.addEventListener('online', onChange)
  window.addEventListener('offline', onChange)
  return () => {
    window.removeEventListener('online', onChange)
    window.removeEventListener('offline', onChange)
  }
}

/**
 * Owns the single protocol connection for the whole app.
 *
 * One client instance, created once and kept in state (not a module singleton),
 * so React Strict Mode's double-mount and Fast Refresh do not leave a second
 * socket running. Everything downstream reads it from context.
 */
export function SyncAppProvider({ children }: { children: ReactNode }) {
  const [client] = useState(
    () =>
      new SyncAppClient({
        url: config.gatewayUrl,
        clientVersion: config.clientVersion,
      }),
  )
  // Subscribed rather than mirrored into state: the client is an external store,
  // and useSyncExternalStore keeps the render consistent with it without an
  // effect that writes state on mount.
  const state = useSyncExternalStore(
    useCallback((onChange) => client.on('state', onChange), [client]),
    () => client.state,
    () => 'idle' as ConnectionState,
  )

  const online = useSyncExternalStore(
    subscribeToConnectivity,
    () => navigator.onLine,
    () => true,
  )

  useEffect(() => {
    client.setDeviceId(getDeviceId())
  }, [client])

  const value = useMemo<SyncAppContextValue>(
    () => ({ client, state, online }),
    [client, state, online],
  )

  return <SyncAppContext.Provider value={value}>{children}</SyncAppContext.Provider>
}

export function useSyncApp(): SyncAppContextValue {
  const value = useContext(SyncAppContext)
  if (!value) throw new Error('useSyncApp must be used inside <SyncAppProvider>')
  return value
}

export function useSyncAppClient(): SyncAppClient {
  return useSyncApp().client
}

export function useConnectionState(): ConnectionState {
  return useSyncApp().state
}

/** True only when the protocol connection is authenticated and usable. */
export function useIsConnected(): boolean {
  return useSyncApp().state === 'ready'
}
