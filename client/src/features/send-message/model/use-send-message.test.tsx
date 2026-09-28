import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useChatStore } from '@/entities/chat'
import { flattenHistory, type HistoryData } from '@/entities/message'
import { useSessionStore } from '@/entities/session'
import { ErrorCode, MsgType, queryKeys } from '@/shared/api'
import {
  flush,
  installFakeSocket,
  renderHookWithProvider,
  type Harness,
} from '@/shared/api/syncapp/test-harness'
import { useOutboxStore } from './outbox'
import { isHandleTarget, useOutboxFlush, useSendMessage } from './use-send-message'

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
  useOutboxStore.getState().reset()
  useOutboxStore.getState().load('self')
  useChatStore.getState().reset()
  useChatStore.getState().load('self')
  useSessionStore.setState({ status: 'authenticated', session: SELF })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

/** The SEND frame the client last wrote, with its correlation id. */
function lastSend(harness: Harness<unknown>) {
  return harness.socket.frames.filter((frame) => frame.type === MsgType.SEND).at(-1)
}

/** Answers the pending SEND with a SEND_ACK the way the gateway would. */
async function ack(
  harness: Harness<unknown>,
  body: { chatId: string; messageId: string; chatSeq: number; timestamp?: number },
) {
  const requestId = harness.socket.envelopes.at(-1)!.requestId
  // Echo back the dedup key the client actually sent — that is what the ack
  // correlates against on the client side.
  const dedupKey = (lastSend(harness)?.body as { dedupKey: string }).dedupKey
  await act(async () => {
    harness.socket.deliver(
      MsgType.SEND_ACK,
      { dedupKey, timestamp: 1_000, ...body },
      { requestId },
    )
    await flush()
  })
}

async function reject(
  harness: Harness<unknown>,
  code: number,
  message = 'nope',
  retryAfterMs = 0,
) {
  const requestId = harness.socket.envelopes.at(-1)!.requestId
  await act(async () => {
    harness.socket.deliver(MsgType.ERROR, { code, message, retryAfterMs }, { requestId })
    await flush()
  })
}

describe('isHandleTarget', () => {
  it('recognises a handle', () => {
    // A handle has no chat id yet — the gateway creates the direct chat on the
    // first message addressed to it.
    expect(isHandleTarget('@bob')).toBe(true)
  })

  it('does not treat a chat id as a handle', () => {
    expect(isHandleTarget('c1')).toBe(false)
  })

  it('does not treat an empty target as a handle', () => {
    expect(isHandleTarget('')).toBe(false)
  })
})

describe('useSendMessage', () => {
  it('queues the message in the outbox', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))

    expect(useOutboxStore.getState().items).toHaveLength(1)
    expect(useOutboxStore.getState().items[0].text).toBe('hello')
  })

  /**
   * The optimistic row is what makes the composer feel instant. It is keyed by
   * the dedup key because the server id does not exist yet — and that same key
   * is what lets the SEND_ACK collapse the two rows instead of showing the
   * message twice.
   */
  it('renders the message immediately, before any acknowledgement', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))

    const history = flattenHistory(
      harness.queryClient.getQueryData<HistoryData>(queryKeys.history('c1')),
    )
    expect(history).toHaveLength(1)
    expect(history[0]).toMatchObject({ text: 'hello', status: 'pending', outgoing: true })
  })

  it('keys the optimistic row on the dedup key, since no server id exists yet', async () => {
    // That key is also what lets the SEND_ACK collapse the two rows into one
    // instead of leaving the message on screen twice.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))

    const dedupKey = useOutboxStore.getState().items[0].dedupKey
    const history = flattenHistory(
      harness.queryClient.getQueryData<HistoryData>(queryKeys.history('c1')),
    )
    expect(history[0].id).toBe(dedupKey)
  })

  it('collapses the optimistic row into the acknowledged one', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await ack(harness, { chatId: 'c1', messageId: 'server-1', chatSeq: 7 })

    const history = flattenHistory(
      harness.queryClient.getQueryData<HistoryData>(queryKeys.history('c1')),
    )
    expect(history).toHaveLength(1)
    expect(history[0]).toMatchObject({ id: 'server-1', seq: 7, status: 'sent' })
  })

  it('shows a permanently rejected message as failed, with its reason', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await reject(harness, ErrorCode.FORBIDDEN, 'you are blocked')

    const history = flattenHistory(
      harness.queryClient.getQueryData<HistoryData>(queryKeys.history('c1')),
    )
    expect(history[0]).toMatchObject({ status: 'failed', error: 'you are blocked' })
  })

  it('sends a SEND frame carrying the whole payload', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() =>
      harness.result.current.send('hello', {
        replyTo: 'm7',
        ttlSeconds: 60,
        attachment: {
          kind: 'image',
          mediaRef: 'media-1',
          filename: 'cat.png',
          mime: 'image/png',
          size: 10,
          durationMs: 0,
          waveform: [],
          width: 1,
          height: 1,
          thumbRef: '',
        },
      }),
    )

    expect(lastSend(harness)?.body).toMatchObject({
      chatId: 'c1',
      text: 'hello',
      replyTo: 'm7',
      ttlSeconds: 60,
      mediaRef: 'media-1',
    })
  })

  it('correlates the send so the ack can be matched', async () => {
    // SEND is the one message type that is a request, not fire-and-forget: the
    // durable id and chat sequence only come back in its reply.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current.send('hello'))

    expect(harness.socket.envelopes.at(-1)!.requestId).not.toBe(0)
  })

  it('carries a dedup key, which is what makes a replay safe', async () => {
    // The server resolves a repeat of (sender, dedup_key) to the stored message,
    // so the reconnect flush can replay the queue blindly.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current.send('hello'))

    const body = lastSend(harness)?.body as { dedupKey: string }
    expect(body.dedupKey).toBeTruthy()
    expect(body.dedupKey).toBe(useOutboxStore.getState().items[0].dedupKey)
  })

  it('trims the text', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current.send('  hello  '))

    expect((lastSend(harness)?.body as { text: string }).text).toBe('hello')
  })

  it('ignores a message that is only whitespace', async () => {
    // Otherwise every stray Enter in the composer posts an empty bubble.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('   '))

    expect(useOutboxStore.getState().items).toHaveLength(0)
  })

  it('sends an attachment with no caption', async () => {
    // A photo with no text is a perfectly ordinary message.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() =>
      harness.result.current.send('', {
        attachment: {
          kind: 'image',
          mediaRef: 'media-1',
          filename: 'cat.png',
          mime: 'image/png',
          size: 10,
          durationMs: 0,
          waveform: [],
          width: 1,
          height: 1,
          thumbRef: '',
        },
      }),
    )

    expect(useOutboxStore.getState().items).toHaveLength(1)
  })

  it('sends nothing when there is no session', async () => {
    useSessionStore.setState({ status: 'anonymous', session: null })
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))

    expect(useOutboxStore.getState().items).toHaveLength(0)
  })

  /**
   * Queueing while offline is the whole point of the outbox: the message is
   * persisted with its dedup key and replayed on reconnect, so it survives a
   * reload, a crashed tab, or a laptop closed mid-send.
   */
  it('queues while disconnected without attempting a send', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))

    act(() => harness.result.current.send('hello'))

    expect(useOutboxStore.getState().items).toHaveLength(1)
    expect(useOutboxStore.getState().items[0].status).toBe('pending')
  })

  it('removes the item from the outbox once acknowledged', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await ack(harness, { chatId: 'c1', messageId: 'server-1', chatSeq: 7 })

    expect(useOutboxStore.getState().items).toHaveLength(0)
  })

  it('folds the acknowledged message into the chat summary', async () => {
    // The chat list preview and the read cursor both come from this.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await ack(harness, { chatId: 'c1', messageId: 'server-1', chatSeq: 7 })

    const chat = useChatStore.getState().chats.c1
    expect(chat.lastSeq).toBe(7)
    expect(chat.lastMessage?.text).toBe('hello')
  })

  it('treats our own acknowledged message as read', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await ack(harness, { chatId: 'c1', messageId: 'server-1', chatSeq: 7 })

    expect(useChatStore.getState().chats.c1.lastReadSeq).toBe(7)
  })

  /**
   * A direct chat has no id until its first message lands, so the message is
   * addressed to "@bob" and only the ack reveals where it went. Without the
   * hand-off the conversation would stay filed under the handle and the real
   * chat would look empty.
   */
  it('registers the chat the gateway created for a handle', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('@bob'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await ack(harness, { chatId: 'real-chat', messageId: 'server-1', chatSeq: 1 })

    expect(useChatStore.getState().chats['real-chat']).toMatchObject({
      kind: 'direct',
      handle: '@bob',
      provisional: false,
    })
  })

  it('reports the resolved chat id to its caller', async () => {
    // The chat screen is mounted on the handle route and has to navigate itself
    // to the real chat once one exists.
    const resolved: string[] = []
    const harness = await renderHookWithProvider(() =>
      useSendMessage('@bob', (chatId) => resolved.push(chatId)),
    )
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await ack(harness, { chatId: 'real-chat', messageId: 'server-1', chatSeq: 1 })

    expect(resolved).toEqual(['real-chat'])
  })

  it('does not report a resolution when the target was already a chat id', async () => {
    const resolved: string[] = []
    const harness = await renderHookWithProvider(() =>
      useSendMessage('c1', (chatId) => resolved.push(chatId)),
    )
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await ack(harness, { chatId: 'c1', messageId: 'server-1', chatSeq: 1 })

    expect(resolved).toEqual([])
  })

  /**
   * The retryable/permanent split decides whether the user ever sees a failure.
   * A throttle or a server blip resolves itself on the next reconnect, so
   * surfacing it would be noise; a FORBIDDEN never will, so hiding it would
   * leave the message queued forever with no explanation.
   */
  it('keeps a throttled message queued and silent', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await reject(harness, ErrorCode.RATE_LIMITED, 'slow down', 2500)

    const item = useOutboxStore.getState().items[0]
    expect(item).toBeDefined()
    expect(item.status).toBe('pending')
  })

  it('keeps a server-error message queued', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await reject(harness, ErrorCode.UNAVAILABLE, 'restarting')

    expect(useOutboxStore.getState().items[0].status).toBe('pending')
  })

  it('marks a forbidden message failed, with the gateway`s reason', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await reject(harness, ErrorCode.FORBIDDEN, 'you are blocked')

    const item = useOutboxStore.getState().items[0]
    expect(item.status).toBe('failed')
    expect(item.error).toBe('you are blocked')
  })

  it('marks a bad-argument message failed', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await reject(harness, ErrorCode.BAD_ARG, 'message too long')

    expect(useOutboxStore.getState().items[0].status).toBe('failed')
  })

  it('persists a failure, so a reload does not silently retry it', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    await reject(harness, ErrorCode.FORBIDDEN, 'blocked')

    useOutboxStore.getState().reset()
    useOutboxStore.getState().load('self')

    expect(useOutboxStore.getState().items[0]).toMatchObject({
      status: 'failed',
      error: 'blocked',
    })
  })
})

describe('retry', () => {
  it('re-sends a failed message with its original dedup key', async () => {
    // The key is what makes the retry idempotent — the server resolves it to the
    // stored message if the first attempt actually landed.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    const dedupKey = useOutboxStore.getState().items[0].dedupKey
    await reject(harness, ErrorCode.FORBIDDEN, 'blocked')
    harness.socket.sent.length = 0

    act(() =>
      harness.result.current.retry({
        id: dedupKey,
        chatId: 'c1',
        senderId: 'self',
        text: 'hello',
        seq: 0,
        timestamp: 1,
        edited: false,
        deleted: false,
        replyTo: '',
        mediaRef: '',
        attachment: null,
        forward: null,
        threadRoot: '',
        replyCount: 0,
        expiresAt: 0,
        outgoing: true,
        status: 'failed',
        dedupKey,
      }),
    )

    expect((lastSend(harness)?.body as { dedupKey: string }).dedupKey).toBe(dedupKey)
  })

  it('moves the item back to pending and clears the error', async () => {
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    act(() => harness.result.current.send('hello'))
    const dedupKey = useOutboxStore.getState().items[0].dedupKey
    await reject(harness, ErrorCode.FORBIDDEN, 'blocked')

    act(() =>
      harness.result.current.retry({
        id: dedupKey,
        chatId: 'c1',
        senderId: 'self',
        text: 'hello',
        seq: 0,
        timestamp: 1,
        edited: false,
        deleted: false,
        replyTo: '',
        mediaRef: '',
        attachment: null,
        forward: null,
        threadRoot: '',
        replyCount: 0,
        expiresAt: 0,
        outgoing: true,
        status: 'failed',
        dedupKey,
      }),
    )

    const item = useOutboxStore.getState().items[0]
    expect(item.status).toBe('pending')
    expect(item.error).toBeUndefined()
  })

  it('ignores a retry for a message that is no longer queued', async () => {
    // Two tabs, or a retry tapped after the flush already succeeded.
    const harness = await renderHookWithProvider(() => useSendMessage('c1'))
    await harness.connect()

    expect(() =>
      act(() =>
        harness.result.current.retry({
          id: 'gone',
          chatId: 'c1',
          senderId: 'self',
          text: 'hello',
          seq: 0,
          timestamp: 1,
          edited: false,
          deleted: false,
          replyTo: '',
          mediaRef: '',
          attachment: null,
          forward: null,
          threadRoot: '',
          replyCount: 0,
          expiresAt: 0,
          outgoing: true,
          status: 'failed',
          dedupKey: 'gone',
        }),
      ),
    ).not.toThrow()
  })
})

describe('useOutboxFlush', () => {
  /** Puts an item in the queue as if a previous session had left it there. */
  function queue(text: string, overrides: Record<string, unknown> = {}) {
    useOutboxStore.getState().enqueue({
      dedupKey: `key-${text}`,
      target: 'c1',
      text,
      createdAt: 1_000,
      status: 'pending',
      ...overrides,
    })
  }

  it('sends nothing while disconnected', async () => {
    queue('queued')
    const harness = await renderHookWithProvider(() => useOutboxFlush())

    expect(harness.result.current).toBeUndefined()
    expect(useOutboxStore.getState().items).toHaveLength(1)
  })

  it('replays a queued message once the connection comes back', async () => {
    // This is what makes "write it offline, it sends itself later" true.
    queue('queued')
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()

    const sent = harness.socket.frames.filter((frame) => frame.type === MsgType.SEND)
    expect(sent).toHaveLength(1)
    expect((sent[0].body as { text: string }).text).toBe('queued')
  })

  it('replays with the original dedup key', async () => {
    queue('queued')
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()

    const sent = harness.socket.frames.filter((frame) => frame.type === MsgType.SEND)
    expect((sent[0].body as { dedupKey: string }).dedupKey).toBe('key-queued')
  })

  it('drops an item from the queue once acknowledged', async () => {
    queue('queued')
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()
    await ack(harness, { chatId: 'c1', messageId: 'server-1', chatSeq: 3 })

    expect(useOutboxStore.getState().items).toHaveLength(0)
  })

  it('skips items already marked failed', async () => {
    // A permanent rejection is not retried automatically; only the user's retry
    // button moves it back to pending.
    queue('bad', { status: 'failed', error: 'blocked' })
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()

    expect(harness.socket.frames.filter((frame) => frame.type === MsgType.SEND)).toHaveLength(0)
  })

  it('does nothing for an empty queue', async () => {
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()

    expect(harness.socket.frames.filter((frame) => frame.type === MsgType.SEND)).toHaveLength(0)
  })

  it('marks a permanently rejected item failed', async () => {
    queue('queued')
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()
    await reject(harness, ErrorCode.FORBIDDEN, 'blocked')

    expect(useOutboxStore.getState().items[0]).toMatchObject({
      status: 'failed',
      error: 'blocked',
    })
  })

  /**
   * Stopping on the first transient failure is deliberate: a queue that failed
   * once has almost certainly lost the connection again, and walking the rest of
   * it would produce one timeout per item — several minutes of spinner for a
   * socket that is already gone.
   */
  it('stops the run after a transient failure rather than hammering', async () => {
    queue('first')
    queue('second')
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()
    await reject(harness, ErrorCode.UNAVAILABLE, 'restarting')

    const sent = harness.socket.frames.filter((frame) => frame.type === MsgType.SEND)
    expect(sent).toHaveLength(1)
    expect(useOutboxStore.getState().items).toHaveLength(2)
  })

  it('leaves a transiently failed item queued and still pending', async () => {
    queue('queued')
    const harness = await renderHookWithProvider(() => useOutboxFlush())
    await harness.connect()
    await reject(harness, ErrorCode.UNAVAILABLE, 'restarting')

    expect(useOutboxStore.getState().items[0].status).toBe('pending')
  })
})
