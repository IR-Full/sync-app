import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useChatStore, useReceiptStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { useSettingsStore } from '@/entities/settings'
import { MsgType } from '@/shared/api'
import { installFakeSocket, renderHookWithProvider } from '@/shared/api/syncapp/test-harness'
import { useMarkRead, usePeerReadSeq, useReceiptReset } from './use-read-receipts'

const SELF = {
  userId: 'self',
  username: 'alice',
  deviceId: 'd1',
  sessionId: 's1',
  token: 't',
  resumeToken: 'r',
}

beforeEach(() => {
  installFakeSocket()
  localStorage.clear()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useReceiptStore.getState().clear()
  useChatStore.getState().reset()
  useChatStore.getState().load('self')
  useSessionStore.setState({ status: 'authenticated', session: SELF })
  useSettingsStore.getState().hydrate()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const readFrames = (harness: { socket: { frames: { type: number; body: unknown }[] } }) =>
  harness.socket.frames.filter((frame) => frame.type === MsgType.READ)

describe('useMarkRead', () => {
  it('sends a READ frame with the cursor', async () => {
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(5))

    expect(harness.socket.lastOf(MsgType.READ)?.body).toMatchObject({
      chatId: 'c1',
      upToChatSeq: 5,
    })
  })

  it('sends fire-and-forget, with no request id', async () => {
    // The gateway answers READ only on failure.
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(5))

    expect(harness.socket.envelopes.at(-1)!.requestId).toBe(0)
  })

  /**
   * The server never reports our *own* read cursor back — there is no message
   * that asks for it — so the local registry is the only copy this client will
   * ever have. A mark-read that only sent the frame would leave the unread badge
   * on screen until the chat was reloaded from scratch.
   */
  it('advances the local cursor', async () => {
    useChatStore.getState().upsert({ id: 'c1', lastSeq: 9 })
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()

    act(() => harness.result.current(9))

    expect(useChatStore.getState().chats.c1.lastReadSeq).toBe(9)
  })

  it('advances the local cursor even while disconnected', async () => {
    // Reading is a fact about this user; being offline does not un-read it.
    useChatStore.getState().upsert({ id: 'c1', lastSeq: 9 })
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))

    act(() => harness.result.current(9))

    expect(useChatStore.getState().chats.c1.lastReadSeq).toBe(9)
  })

  it('does not resend a cursor it has already sent', async () => {
    // The list fires this on every scroll frame; resending the same cursor would
    // spend rate-limit budget on a no-op the server ignores anyway.
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => {
      harness.result.current(5)
      harness.result.current(5)
      harness.result.current(3)
    })

    expect(readFrames(harness)).toHaveLength(1)
  })

  it('sends a higher cursor', async () => {
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => {
      harness.result.current(5)
      harness.result.current(9)
    })

    expect(readFrames(harness)).toHaveLength(2)
    expect(harness.socket.lastOf(MsgType.READ)?.body).toMatchObject({ upToChatSeq: 9 })
  })

  it('ignores a zero or negative cursor', async () => {
    // Sequence numbers start at 1; 0 means "nothing read", which is the absence
    // of a receipt rather than a receipt for message zero.
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => {
      harness.result.current(0)
      harness.result.current(-1)
    })

    expect(readFrames(harness)).toHaveLength(0)
  })

  it('ignores a call with no chat id', async () => {
    const harness = await renderHookWithProvider(() => useMarkRead(''))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(5))

    expect(readFrames(harness)).toHaveLength(0)
  })

  /**
   * Turning receipts off is a privacy setting: it stops *others* seeing your
   * ticks. It must not stop your own unread badge from clearing, or the setting
   * would quietly break the app for the user who enabled it.
   */
  it('sends nothing when receipts are turned off', async () => {
    useSettingsStore.getState().set('sendReadReceipts', false)
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current(5))

    expect(readFrames(harness)).toHaveLength(0)
  })

  it('still clears the local badge when receipts are turned off', async () => {
    useSettingsStore.getState().set('sendReadReceipts', false)
    useChatStore.getState().upsert({ id: 'c1', lastSeq: 5 })
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()

    act(() => harness.result.current(5))

    expect(useChatStore.getState().chats.c1.lastReadSeq).toBe(5)
  })

  it('sends nothing while disconnected', async () => {
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))

    expect(() => act(() => harness.result.current(5))).not.toThrow()
  })

  it('swallows a send failure', async () => {
    // The next read in this chat re-sends a higher cursor, so there is nothing
    // to recover and nothing worth telling the user.
    const harness = await renderHookWithProvider(() => useMarkRead('c1'))
    await harness.connect()
    vi.spyOn(harness.client, 'send').mockImplementation(() => {
      throw new Error('socket went away')
    })

    expect(() => act(() => harness.result.current(5))).not.toThrow()
  })
})

describe('usePeerReadSeq', () => {
  it('reports the highest cursor another member has reached', async () => {
    const harness = await renderHookWithProvider(() => usePeerReadSeq('c1'))

    act(() => {
      useReceiptStore.getState().apply('c1', 'u2', 4)
      useReceiptStore.getState().apply('c1', 'u3', 9)
    })

    expect(harness.result.current).toBe(9)
  })

  it('excludes our own cursor', async () => {
    // Counting it would tick every message the moment we opened the chat.
    const harness = await renderHookWithProvider(() => usePeerReadSeq('c1'))

    act(() => useReceiptStore.getState().apply('c1', 'self', 99))

    expect(harness.result.current).toBe(0)
  })

  it('reports 0 for a chat with no receipts', async () => {
    const harness = await renderHookWithProvider(() => usePeerReadSeq('c1'))
    expect(harness.result.current).toBe(0)
  })

  it('is scoped to one chat', async () => {
    const harness = await renderHookWithProvider(() => usePeerReadSeq('c2'))

    act(() => useReceiptStore.getState().apply('c1', 'u2', 9))

    expect(harness.result.current).toBe(0)
  })
})

describe('useReceiptReset', () => {
  /**
   * Receipts are a live-session view: the protocol pushes other users' cursors
   * and never lets a client query them, so anything held across a disconnect is
   * a claim the client can no longer support.
   */
  it('clears receipts when the connection drops', async () => {
    useReceiptStore.getState().apply('c1', 'u2', 9)
    const harness = await renderHookWithProvider(() => useReceiptReset(false))

    expect(useReceiptStore.getState().cursors).toEqual({})
    harness.unmount()
  })

  it('keeps receipts while connected', async () => {
    const harness = await renderHookWithProvider(() => useReceiptReset(true))
    act(() => useReceiptStore.getState().apply('c1', 'u2', 9))

    expect(useReceiptStore.getState().cursors.c1.u2).toBe(9)
    harness.unmount()
  })
})
