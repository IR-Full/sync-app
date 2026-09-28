import { renderHook } from '@testing-library/react'
import { act, type ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StorageKeys, readStorage } from '../../lib/storage'
import { FakeSocket } from '../protocol/fake-socket'
import { MsgType } from '../protocol'
import {
  SyncAppProvider,
  useConnectionState,
  useIsConnected,
  useSyncApp,
  useSyncAppClient,
} from './context'

const wrapper = ({ children }: { children: ReactNode }) => (
  <SyncAppProvider>{children}</SyncAppProvider>
)

/** Drains microtasks; works under fake timers, unlike a 0ms timeout. */
async function flush() {
  for (let i = 0; i < 12; i++) await Promise.resolve()
}

beforeEach(() => {
  FakeSocket.reset()
  localStorage.clear()
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('useSyncApp', () => {
  it('throws outside the provider', () => {
    // A hook that silently returned null here would fail much later, inside
    // whichever feature first tried to send something.
    expect(() => renderHook(() => useSyncApp())).toThrow(/SyncAppProvider/)
  })

  it('exposes a client inside the provider', () => {
    const { result } = renderHook(() => useSyncAppClient(), { wrapper })
    expect(result.current).toBeDefined()
  })

  /**
   * The client is held in state, not in a module singleton. React Strict Mode
   * double-mounts in development and Fast Refresh remounts on every save; a
   * module-level instance would leave a second socket connected and receiving
   * every frame twice.
   */
  it('keeps one client across re-renders', () => {
    const { result, rerender } = renderHook(() => useSyncAppClient(), { wrapper })
    const first = result.current

    rerender()

    expect(result.current).toBe(first)
  })

  it('gives separate provider trees separate clients', () => {
    // Two providers means two connections, which is why there is exactly one in
    // the app — but the instance must at least belong to its own tree.
    const a = renderHook(() => useSyncAppClient(), { wrapper })
    const b = renderHook(() => useSyncAppClient(), { wrapper })

    expect(a.result.current).not.toBe(b.result.current)
  })
})

describe('useConnectionState', () => {
  it('starts idle', () => {
    const { result } = renderHook(() => useConnectionState(), { wrapper })
    expect(result.current).toBe('idle')
  })

  /**
   * Subscribed through useSyncExternalStore rather than mirrored into state with
   * an effect: the client is an external store, and mirroring would render one
   * frame behind it — long enough for the composer to look enabled on a
   * connection that has already dropped.
   */
  it('tracks the client as it connects', async () => {
    const { result } = renderHook(
      () => ({ state: useConnectionState(), client: useSyncAppClient() }),
      { wrapper },
    )

    const pending = result.current.client.connect({ kind: 'token', token: 't' })
    const socket = FakeSocket.last

    await act(async () => {
      socket.open()
      socket.deliver(
        MsgType.WELCOME,
        {
          serverVersion: 'test/1',
          sessionId: 's1',
          caps: 0b10100,
          heartbeatMs: 20_000,
          maxInflight: 32,
          resumeSupported: true,
        },
        { requestId: socket.envelopes.at(-1)!.requestId },
      )
      await flush()
      socket.deliver(
        MsgType.AUTH_OK,
        {
          userId: 'u1',
          deviceId: 'dev-1',
          sessionId: 's1',
          token: 'tok-1',
          resumeToken: 'resume-1',
          username: 'alice',
          displayName: 'Alice',
          avatarRef: '',
        },
        { requestId: socket.envelopes.at(-1)!.requestId },
      )
      await pending
      await flush()
    })

    expect(result.current.state).toBe('ready')
  })

  it('reports connecting as soon as a dial starts', async () => {
    const { result } = renderHook(
      () => ({ state: useConnectionState(), client: useSyncAppClient() }),
      { wrapper },
    )

    await act(async () => {
      void result.current.client.connect({ kind: 'token', token: 't' }).catch(() => {})
      await flush()
    })

    expect(result.current.state).toBe('connecting')
  })
})

describe('useIsConnected', () => {
  it('is false before the connection is usable', () => {
    // 'connecting' and 'reconnecting' are not usable states; treating them as
    // connected would let the composer send into a socket that rejects it.
    const { result } = renderHook(() => useIsConnected(), { wrapper })
    expect(result.current).toBe(false)
  })

  it('is false while merely dialling', async () => {
    const { result } = renderHook(
      () => ({ connected: useIsConnected(), client: useSyncAppClient() }),
      { wrapper },
    )

    await act(async () => {
      void result.current.client.connect({ kind: 'token', token: 't' }).catch(() => {})
      await flush()
    })

    expect(result.current.connected).toBe(false)
  })
})

describe('device id', () => {
  /**
   * The gateway keys sessions, multi-device delivery and E2E key bundles on the
   * device id. Failing to set it would make every page load look like a
   * brand-new device — splitting one browser across many device rows and
   * scattering secret-chat sessions across them.
   */
  it('gives the client the stable per-installation id', async () => {
    const { result } = renderHook(
      () => ({ client: useSyncAppClient(), state: useConnectionState() }),
      { wrapper },
    )

    await act(async () => {
      void result.current.client.connect({ kind: 'token', token: 't' }).catch(() => {})
      await flush()
      // HELLO goes out on `onopen`, not on dial.
      FakeSocket.last.open()
      await flush()
    })

    const hello = FakeSocket.last.frames[0].body as { deviceId: string }
    expect(hello.deviceId).toBe(readStorage(StorageKeys.deviceId, ''))
    expect(hello.deviceId).toBeTruthy()
  })
})

describe('browser connectivity', () => {
  it('reports the browser online flag', () => {
    // A different question from "is the socket up": the banner distinguishes
    // "you are offline" from "we are reconnecting", and only the browser knows
    // the first one.
    const { result } = renderHook(() => useSyncApp(), { wrapper })
    expect(result.current.online).toBe(navigator.onLine)
  })

  it('follows the browser going offline', () => {
    const { result } = renderHook(() => useSyncApp(), { wrapper })

    act(() => {
      vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false)
      window.dispatchEvent(new Event('offline'))
    })

    expect(result.current.online).toBe(false)
  })

  it('follows the browser coming back online', () => {
    const { result } = renderHook(() => useSyncApp(), { wrapper })
    const onLine = vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false)

    act(() => window.dispatchEvent(new Event('offline')))
    expect(result.current.online).toBe(false)

    act(() => {
      onLine.mockReturnValue(true)
      window.dispatchEvent(new Event('online'))
    })

    expect(result.current.online).toBe(true)
  })

  it('removes its listeners on unmount', () => {
    // The subscription is armed per provider; leaking it would keep a dead tree
    // subscribed to every connectivity change for the life of the tab.
    const remove = vi.spyOn(window, 'removeEventListener')
    const { unmount } = renderHook(() => useSyncApp(), { wrapper })

    unmount()

    const removed = remove.mock.calls.map((call) => call[0])
    expect(removed).toContain('online')
    expect(removed).toContain('offline')
  })
})
