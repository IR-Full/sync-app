import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useTypingStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { MsgType } from '@/shared/api'
import { FakeSocket } from '@/shared/api/protocol/fake-socket'
import { installFakeSocket, renderHookWithProvider } from '@/shared/api/syncapp/test-harness'
import { config } from '@/shared/config/env'
import { useTypingNotifier, useTypingUsers } from './use-typing'

beforeEach(() => {
  installFakeSocket()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useTypingStore.getState().clear()
  useSessionStore.setState({ status: 'unknown', session: null })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('useTypingNotifier', () => {
  it('sends a TYPING frame for the chat', async () => {
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(true))

    expect(harness.socket.lastOf(MsgType.TYPING)?.body).toMatchObject({
      chatId: 'c1',
      active: true,
    })
  })

  it('sends a stop', async () => {
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(false))

    expect(harness.socket.lastOf(MsgType.TYPING)?.body).toMatchObject({ active: false })
  })

  it('sends fire-and-forget, with no request id', async () => {
    // The gateway answers TYPING only by relaying it, so a correlated request
    // would wait out the full timeout and then reject.
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(true))

    expect(harness.socket.envelopes.at(-1)!.requestId).toBe(0)
  })

  /**
   * The gateway allows roughly one TYPING per chat every two seconds and
   * silently discards the rest. Sending on every keystroke would spend the whole
   * budget on frames that never arrive — so the indicator would flicker off on
   * the receiving side precisely while the user was typing fastest.
   */
  it('throttles repeated starts', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => {
      harness.result.current(true)
      harness.result.current(true)
      harness.result.current(true)
    })

    expect(harness.socket.frames.filter((f) => f.type === MsgType.TYPING)).toHaveLength(1)
  })

  it('sends again once the throttle window has passed', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(true))
    vi.advanceTimersByTime(config.typingThrottleMs + 1)
    act(() => harness.result.current(true))

    expect(harness.socket.frames.filter((f) => f.type === MsgType.TYPING)).toHaveLength(2)
  })

  it('never throttles a stop', async () => {
    // A dropped stop is the one that leaves "typing…" stuck on the peer's screen
    // until its expiry — the visible failure this whole feature guards against.
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => {
      harness.result.current(true)
      harness.result.current(false)
      harness.result.current(false)
    })

    const sent = harness.socket.frames.filter((f) => f.type === MsgType.TYPING)
    expect(sent).toHaveLength(3)
  })

  it('lets a start through immediately after a stop', async () => {
    // The stop resets the throttle, so resuming typing shows the indicator at
    // once rather than after another two seconds of silence.
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => {
      harness.result.current(true)
      harness.result.current(false)
      harness.result.current(true)
    })

    expect(harness.socket.frames.filter((f) => f.type === MsgType.TYPING)).toHaveLength(3)
  })

  it('sends nothing while the connection is not ready', async () => {
    // `client.send` throws when not ready; an unguarded call would surface as an
    // uncaught error on every keystroke typed during a reconnect. Nothing has
    // been dialled at all here, so "no socket exists" is the assertion.
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))

    expect(() => act(() => harness.result.current(true))).not.toThrow()
    expect(FakeSocket.instances).toHaveLength(0)
  })

  it('sends nothing without a chat id', async () => {
    const harness = await renderHookWithProvider(() => useTypingNotifier(''))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(true))

    expect(harness.socket.frames.filter((f) => f.type === MsgType.TYPING)).toHaveLength(0)
  })

  it('swallows a send failure rather than surfacing it', async () => {
    // A dropped indicator is never worth an error banner.
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    vi.spyOn(harness.client, 'send').mockImplementation(() => {
      throw new Error('socket went away')
    })

    expect(() => act(() => harness.result.current(true))).not.toThrow()
  })

  it('keeps a stable identity while its inputs hold', async () => {
    // It is called from an onChange handler; a new function every render would
    // reset the throttle on each keystroke and defeat the whole point.
    const harness = await renderHookWithProvider(() => useTypingNotifier('c1'))
    await harness.connect()
    const before = harness.result.current

    harness.rerender()

    expect(harness.result.current).toBe(before)
  })
})

describe('useTypingUsers', () => {
  it('reports who is typing', async () => {
    const harness = await renderHookWithProvider(() => useTypingUsers('c1'))

    act(() => useTypingStore.getState().mark('c1', 'u2', true))

    expect(harness.result.current).toEqual(['u2'])
  })

  it('excludes ourselves', async () => {
    // Fanout echoes our own TYPING back; "you are typing" in your own header is
    // pure noise.
    useSessionStore.setState({
      status: 'authenticated',
      session: {
        userId: 'self',
        username: 'alice',
        deviceId: 'd1',
        sessionId: 's1',
        token: 't',
        resumeToken: 'r',
      },
    })
    const harness = await renderHookWithProvider(() => useTypingUsers('c1'))

    act(() => {
      useTypingStore.getState().mark('c1', 'self', true)
      useTypingStore.getState().mark('c1', 'u2', true)
    })

    expect(harness.result.current).toEqual(['u2'])
  })

  it('reports nobody for a quiet chat', async () => {
    const harness = await renderHookWithProvider(() => useTypingUsers('c1'))
    expect(harness.result.current).toEqual([])
  })

  it('is scoped to one chat', async () => {
    const harness = await renderHookWithProvider(() => useTypingUsers('c2'))

    act(() => useTypingStore.getState().mark('c1', 'u2', true))

    expect(harness.result.current).toEqual([])
  })

  it('drops someone who stopped', async () => {
    const harness = await renderHookWithProvider(() => useTypingUsers('c1'))

    act(() => useTypingStore.getState().mark('c1', 'u2', true))
    act(() => useTypingStore.getState().mark('c1', 'u2', false))

    expect(harness.result.current).toEqual([])
  })
})
