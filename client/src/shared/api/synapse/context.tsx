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
import { SynapseClient, type ConnectionState } from '../protocol'

interface SynapseContextValue {
  client: SynapseClient
  state: ConnectionState
  /** browser-level connectivity, which is a different question from "is the socket up" */
  online: boolean
}

const SynapseContext = createContext<SynapseContextValue | null>(null)

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
export function SynapseProvider({ children }: { children: ReactNode }) {
  const [client] = useState(
    () =>
      new SynapseClient({
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

  const value = useMemo<SynapseContextValue>(
    () => ({ client, state, online }),
    [client, state, online],
  )

  return <SynapseContext.Provider value={value}>{children}</SynapseContext.Provider>
}

export function useSynapse(): SynapseContextValue {
  const value = useContext(SynapseContext)
  if (!value) throw new Error('useSynapse must be used inside <SynapseProvider>')
  return value
}

export function useSynapseClient(): SynapseClient {
  return useSynapse().client
}

export function useConnectionState(): ConnectionState {
  return useSynapse().state
}

/** True only when the protocol connection is authenticated and usable. */
export function useIsConnected(): boolean {
  return useSynapse().state === 'ready'
}
