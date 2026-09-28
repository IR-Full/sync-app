import { act, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/entities/session'
import { MsgType } from '@/shared/api'
import {
  flush,
  installFakeSocket,
  renderHookWithProvider,
  replyToLast,
} from '@/shared/api/syncapp/test-harness'
import { useChatHistory } from './use-history'

const SELF = {
  userId: 'self',
  username: 'alice',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'tok-1',
  resumeToken: 'resume-1',
}

const wireMessage = (seq: number) => ({
  messageId: `m${seq}`,
  chatId: 'c1',
  senderId: 'peer',
  chatSeq: seq,
  text: `message ${seq}`,
  timestamp: 1_700_000_000_000 + seq,
})

beforeEach(() => {
  installFakeSocket()
  localStorage.clear()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useSessionStore.setState({ status: 'authenticated', session: SELF })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('backfill', () => {
  /**
   * The shape this client asks for: it advertises CAP_BATCHING, so the gateway
   * answers a page as ONE frame instead of up to a hundred NEW frames plus a
   * terminator.
   */
  it('reads a whole page from a single HISTORY_PAGE frame', async () => {
    const harness = await renderHookWithProvider(() => useChatHistory('c1'))
    await harness.connect()

    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.HISTORY_PAGE, {
        messages: [wireMessage(3), wireMessage(2), wireMessage(1)],
        chatId: 'c1',
        nextBefore: 1,
        done: true,
      })
    })

    await waitFor(() => expect(harness.result.current.data?.pages ?? []).toHaveLength(1))
    const [page] = harness.result.current.data!.pages
    expect(page.messages).toHaveLength(3)
    expect(page.messages.map((m) => m.text)).toContain('message 2')
    expect(page.done).toBe(true)
  })

  /**
   * The streamed form has to keep working: capabilities are negotiated per
   * connection, and iOS and Android do not ask for batching — so a gateway
   * serving them still answers this way, and a reconnect can change which shape
   * arrives.
   */
  it('still reads a page streamed as NEW frames plus HISTORY_OK', async () => {
    const harness = await renderHookWithProvider(() => useChatHistory('c1'))
    await harness.connect()

    await act(async () => {
      await flush()
      const requestId = harness.socket.envelopes.at(-1)!.requestId
      harness.socket.deliver(MsgType.NEW, wireMessage(2), { requestId })
      harness.socket.deliver(MsgType.NEW, wireMessage(1), { requestId })
      harness.socket.deliver(
        MsgType.HISTORY_OK,
        { chatId: 'c1', nextBefore: 1, done: true },
        { requestId },
      )
    })

    await waitFor(() => expect(harness.result.current.data?.pages ?? []).toHaveLength(1))
    expect(harness.result.current.data!.pages[0].messages).toHaveLength(2)
  })

  it('reports an empty page as done rather than paging forever', async () => {
    const harness = await renderHookWithProvider(() => useChatHistory('c1'))
    await harness.connect()

    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.HISTORY_PAGE, {
        messages: [],
        chatId: 'c1',
        nextBefore: 0,
        done: true,
      })
    })

    await waitFor(() => expect(harness.result.current.data?.pages ?? []).toHaveLength(1))
    const [empty] = harness.result.current.data!.pages
    expect(empty.messages).toHaveLength(0)
    // An empty page must end the pagination, not invite another request for the
    // same cursor forever.
    expect(empty.done).toBe(true)
    expect(harness.result.current.hasNextPage).toBe(false)
  })
})
