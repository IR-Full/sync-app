'use client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, type RenderHookResult } from '@testing-library/react'
import { renderHook } from '@testing-library/react'
import { act, type ReactNode } from 'react'
import { vi } from 'vitest'

import { FakeSocket } from '../protocol/fake-socket'
import { MsgType, type SyncAppClient } from '../protocol'
import { SyncAppProvider, useSyncAppClient } from './context'

/**
 * Renders a hook inside a real `<SyncAppProvider>` whose socket is a `FakeSocket`.
 *
 * Deliberately not a mocked client: these hooks are thin, and almost everything
 * that can go wrong in them is about the *protocol* — the type they send, the
 * body shape, whether they send at all in a given connection state. A stub with
 * a `send: vi.fn()` would assert that the hook called the stub, which is a
 * restatement of the implementation. Driving the real client means a wrong
 * message type or a body the schema rejects fails the test.
 *
 * Not exported from the package barrel; `*.test.ts` only.
 */

const WELCOME = {
  serverVersion: 'test/1',
  sessionId: 's1',
  caps: 0b10100,
  heartbeatMs: 20_000,
  maxInflight: 32,
  resumeSupported: true,
}

const AUTH_OK = {
  userId: 'self',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'tok-1',
  resumeToken: 'resume-1',
  username: 'alice',
  displayName: 'Alice',
  avatarRef: '',
}

/** Drains the microtask queue; works under fake timers, unlike a 0ms timeout. */
export async function flush(): Promise<void> {
  for (let i = 0; i < 12; i++) await Promise.resolve()
}

export function installFakeSocket(): void {
  FakeSocket.reset()
  vi.stubGlobal('WebSocket', FakeSocket)
}

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function Wrapper({ children, queryClient }: { children: ReactNode; queryClient: QueryClient }) {
  return (
    <QueryClientProvider client={queryClient}>
      <SyncAppProvider>{children}</SyncAppProvider>
    </QueryClientProvider>
  )
}

/** Captures the provider's client so a test can drive the connection. */
function useCapturedClient(onClient: (client: SyncAppClient) => void): null {
  const client = useSyncAppClient()
  onClient(client)
  return null
}

function ClientProbe({ onClient }: { onClient: (client: SyncAppClient) => void }) {
  return useCapturedClient(onClient)
}

export interface Harness<T> {
  result: RenderHookResult<T, unknown>['result']
  rerender: RenderHookResult<T, unknown>['rerender']
  unmount: () => void
  client: SyncAppClient
  socket: FakeSocket
  /** The same cache the hooks under test write their optimistic rows into. */
  queryClient: QueryClient
  /** Walks the client through HELLO/WELCOME → AUTH/AUTH_OK so `state` is 'ready'. */
  connect: () => Promise<void>
}

export async function renderHookWithProvider<T>(hook: () => T): Promise<Harness<T>> {
  let captured: SyncAppClient | null = null
  const queryClient = makeQueryClient()

  const rendered = renderHook(hook, {
    wrapper: ({ children }) => (
      <Wrapper queryClient={queryClient}>
        <ClientProbe onClient={(client) => (captured = client)} />
        {children}
      </Wrapper>
    ),
  })

  // The probe renders in the same pass, so the client is available immediately.
  const client = captured as unknown as SyncAppClient
  if (!client) throw new Error('the provider did not expose a client')

  const connect = async () => {
    const pending = client.connect({ kind: 'token', token: 'test-token' })
    const socket = FakeSocket.last
    await act(async () => {
      socket.open()
      replyToLast(socket, MsgType.WELCOME, WELCOME)
      await flush()
      replyToLast(socket, MsgType.AUTH_OK, AUTH_OK)
      await pending
      await flush()
    })
  }

  return {
    result: rendered.result,
    rerender: rendered.rerender,
    unmount: rendered.unmount,
    client,
    queryClient,
    get socket() {
      return FakeSocket.last
    },
    connect,
  }
}

/**
 * Replies to whatever request the client sent last.
 *
 * The request counter is per client rather than per connection, so a redial's
 * HELLO is not request 1 — a hard-coded id would fail to correlate and the test
 * would hang instead of failing.
 */
export function replyToLast(socket: FakeSocket, type: number, body?: object | null): void {
  socket.deliver(type, body, { requestId: socket.envelopes.at(-1)!.requestId })
}

/** Renders a component tree inside the same provider stack. */
export function renderWithProvider(ui: ReactNode) {
  const queryClient = makeQueryClient()
  return { ...render(<Wrapper queryClient={queryClient}>{ui}</Wrapper>), queryClient }
}
