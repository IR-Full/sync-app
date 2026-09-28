import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useChatStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { ErrorCode, MsgType } from '@/shared/api'
import { FakeSocket } from '@/shared/api/protocol/fake-socket'
import {
  flush,
  installFakeSocket,
  renderHookWithProvider,
  type Harness,
} from '@/shared/api/syncapp/test-harness'
import { useChatListSync } from './use-chat-list-sync'

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
  useChatStore.getState().reset()
  useChatStore.getState().load('self')
  useSessionStore.setState({ status: 'authenticated', session: SELF })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const chatListRequests = (harness: Harness<void>) =>
  harness.socket.frames.filter((frame) => frame.type === MsgType.CHAT_LIST)

/** Answers the outstanding CHAT_LIST with one page of rows. */
async function respond(
  harness: Harness<void>,
  body: {
    chats: Record<string, unknown>[]
    done?: boolean
    nextAfter?: string
  },
) {
  const requestId = harness.socket.envelopes.at(-1)!.requestId
  await act(async () => {
    harness.socket.deliver(MsgType.CHATS, { done: true, nextAfter: '', ...body }, { requestId })
    await flush()
  })
}

async function fail(harness: Harness<void>, code = ErrorCode.UNSUPPORTED) {
  const requestId = harness.socket.envelopes.at(-1)!.requestId
  await act(async () => {
    harness.socket.deliver(MsgType.ERROR, { code, message: 'nope' }, { requestId })
    await flush()
  })
}

describe('useChatListSync', () => {
  /**
   * Every other path in this client learns about a chat as a consequence of
   * traffic. That is useless for a browser that has never seen any: a fresh
   * install, a cleared localStorage, or a second browser on the same account all
   * start blank with no way to discover the conversations already in progress.
   */
  it('asks for the chat list once connected', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    expect(chatListRequests(harness)).toHaveLength(1)
  })

  it('asks for nothing before the connection is up', async () => {
    // Nothing is even dialled: the effect is gated on a ready connection, so a
    // premature request would throw rather than merely be early.
    await renderHookWithProvider(() => useChatListSync())
    await act(async () => {
      await flush()
    })

    expect(FakeSocket.instances).toHaveLength(0)
  })

  it('asks for nothing without a session', async () => {
    useSessionStore.setState({ status: 'anonymous', session: null })
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    expect(chatListRequests(harness)).toHaveLength(0)
  })

  it('records the chats the server enumerated', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, {
      chats: [
        { chatId: 'c1', type: 'group', title: 'Team', ownerId: 'u9', lastSeq: 5 },
        { chatId: 'c2', type: 'direct', peerId: 'u2', lastSeq: 3 },
      ],
    })

    expect(useChatStore.getState().chats.c1).toMatchObject({
      kind: 'group',
      title: 'Team',
      ownerId: 'u9',
      lastSeq: 5,
    })
    expect(useChatStore.getState().chats.c2).toMatchObject({
      kind: 'direct',
      peerUserId: 'u2',
    })
  })

  it('marks an enumerated chat as real, not provisional', async () => {
    // It demonstrably exists server-side, so it is no longer a placeholder built
    // from a contact entry.
    useChatStore.getState().upsert({ id: 'c1', provisional: true })
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [{ chatId: 'c1', type: 'group', lastSeq: 0 }] })

    expect(useChatStore.getState().chats.c1.provisional).toBe(false)
  })

  it('records a public handle with its sigil', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, {
      chats: [{ chatId: 'c1', type: 'channel', username: 'news', lastSeq: 0 }],
    })

    expect(useChatStore.getState().chats.c1.handle).toBe('@news')
  })

  it('skips a row with no chat id', async () => {
    // A malformed row must not create an entry keyed by "".
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [{ chatId: '', type: 'group', lastSeq: 0 }] })

    expect(Object.keys(useChatStore.getState().chats)).toEqual([])
  })

  /**
   * The merge is an upsert precisely so a page of server rows cannot discard
   * what the cache knows and the server does not send.
   */
  it('keeps the locally-known message preview', async () => {
    useChatStore.getState().applyMessage(
      {
        messageId: 'm1',
        chatId: 'c1',
        senderId: 'u2',
        chatSeq: 9,
        text: 'local preview',
        timestamp: 1_000,
        deleted: false,
      } as never,
      'self',
    )
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [{ chatId: 'c1', type: 'group', lastSeq: 9 }] })

    expect(useChatStore.getState().chats.c1.lastMessage?.text).toBe('local preview')
  })

  it('keeps our own read cursor', async () => {
    // The server does not report it back, so a replace would resurrect an unread
    // badge the user had already cleared.
    useChatStore.getState().upsert({ id: 'c1', lastSeq: 9 })
    useChatStore.getState().markRead('c1', 9)
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [{ chatId: 'c1', type: 'group', lastSeq: 9 }] })

    expect(useChatStore.getState().chats.c1.lastReadSeq).toBe(9)
  })

  /**
   * A page computed a moment before a live NEW frame landed carries a stale
   * `lastSeq`. Unread is derived from it, so letting it win would show a badge
   * that un-counts itself as the list syncs.
   */
  it('never walks the sequence counter backwards', async () => {
    useChatStore.getState().upsert({ id: 'c1', lastSeq: 12 })
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [{ chatId: 'c1', type: 'group', lastSeq: 9 }] })

    expect(useChatStore.getState().chats.c1.lastSeq).toBe(12)
  })

  it('takes a higher sequence from the server', async () => {
    // A chat that received messages while this tab was closed.
    useChatStore.getState().upsert({ id: 'c1', lastSeq: 3 })
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [{ chatId: 'c1', type: 'group', lastSeq: 9 }] })

    expect(useChatStore.getState().chats.c1.lastSeq).toBe(9)
  })

  it('walks to the next page when the server says there is more', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, {
      chats: [{ chatId: 'c1', type: 'group', lastSeq: 0 }],
      done: false,
      nextAfter: 'cursor-1',
    })
    await respond(harness, {
      chats: [{ chatId: 'c2', type: 'group', lastSeq: 0 }],
      done: true,
    })

    expect(Object.keys(useChatStore.getState().chats).sort()).toEqual(['c1', 'c2'])
    expect(chatListRequests(harness)).toHaveLength(2)
  })

  it('passes the cursor back on the next page', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [], done: false, nextAfter: 'cursor-1' })

    expect(chatListRequests(harness).at(-1)?.body).toMatchObject({ after: 'cursor-1' })
  })

  it('stops when the server reports the list complete', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [], done: true, nextAfter: 'ignored' })

    expect(chatListRequests(harness)).toHaveLength(1)
  })

  it('stops when the cursor stops advancing', async () => {
    // A server that kept returning the same cursor would otherwise spin this
    // loop against the gateway forever.
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [], done: false, nextAfter: '' })

    expect(chatListRequests(harness)).toHaveLength(1)
  })

  it('stops when the cursor repeats itself', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await respond(harness, { chats: [], done: false, nextAfter: 'cursor-1' })
    await respond(harness, { chats: [], done: false, nextAfter: 'cursor-1' })

    expect(chatListRequests(harness)).toHaveLength(2)
  })

  /**
   * An older gateway does not know this message type at all, and a transient
   * failure is not worth an error banner: the locally-assembled list still
   * works, and the next connect tries again.
   */
  it('gives up quietly when the gateway rejects the request', async () => {
    useChatStore.getState().upsert({ id: 'local', kind: 'group', title: 'Known locally' })
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    await fail(harness)

    expect(useChatStore.getState().chats.local).toBeDefined()
    expect(chatListRequests(harness)).toHaveLength(1)
  })

  it('syncs once per connection, not once per render', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()

    harness.rerender()
    await act(async () => {
      await flush()
    })

    expect(chatListRequests(harness)).toHaveLength(1)
  })

  it('does not re-sync after the page completes', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()
    await respond(harness, { chats: [], done: true })

    harness.rerender()
    await act(async () => {
      await flush()
    })

    expect(chatListRequests(harness)).toHaveLength(1)
  })

  /**
   * The account may have been added to a chat from another device while this
   * tab was offline, so a reconnect has to re-enumerate rather than trust the
   * list it built before the drop.
   */
  it('re-syncs after the connection drops and returns', async () => {
    const harness = await renderHookWithProvider(() => useChatListSync())
    await harness.connect()
    await respond(harness, { chats: [], done: true })

    await act(async () => {
      harness.socket.serverClose()
      await flush()
    })
    await harness.connect()

    expect(chatListRequests(harness)).toHaveLength(1)
  })
})
