import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useChatStore, useReceiptStore, useTypingStore } from '@/entities/chat'
import { useDraftStore } from '@/entities/draft'
import { flattenHistory, useReactionStore, type HistoryData } from '@/entities/message'
import { usePollStore } from '@/entities/poll'
import { useSessionStore } from '@/entities/session'
import { useUserDirectory } from '@/entities/user'
import { ErrorCode, MsgType, queryKeys } from '@/shared/api'
import {
  flush,
  installFakeSocket,
  renderHookWithProvider,
  type Harness,
} from '@/shared/api/syncapp/test-harness'
import { useRealtimeSync } from './use-realtime-sync'

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
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  useChatStore.getState().reset()
  useChatStore.getState().load('self')
  useReceiptStore.getState().clear()
  useTypingStore.getState().clear()
  useReactionStore.getState().clear()
  useDraftStore.getState().clear()
  usePollStore.getState().clear()
  useUserDirectory.getState().clear()
  useSessionStore.setState({ status: 'authenticated', session: SELF })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

/** Starts the bridge with a live connection and returns the harness. */
async function bridge(): Promise<Harness<void>> {
  const harness = await renderHookWithProvider(() => useRealtimeSync())
  await harness.connect()
  return harness
}

/** Pushes one unsolicited frame at the client, as the gateway would. */
async function push(harness: Harness<void>, type: number, body: object) {
  await act(async () => {
    harness.socket.deliver(type, body)
    await flush()
  })
}

const history = (harness: Harness<void>, chatId: string) =>
  flattenHistory(harness.queryClient.getQueryData<HistoryData>(queryKeys.history(chatId)))

const NEW_MESSAGE = {
  messageId: 'm1',
  chatId: 'c1',
  senderId: 'u2',
  chatSeq: 7,
  text: 'hello',
  timestamp: 1_000,
}

describe('live messages', () => {
  it('writes an incoming message into the chat history', async () => {
    const harness = await bridge()

    await push(harness, MsgType.NEW, NEW_MESSAGE)

    expect(history(harness, 'c1')).toHaveLength(1)
    expect(history(harness, 'c1')[0]).toMatchObject({ id: 'm1', text: 'hello', seq: 7 })
  })

  it('folds it into the chat summary', async () => {
    // The chat list preview and unread badge both read from here.
    const harness = await bridge()

    await push(harness, MsgType.NEW, NEW_MESSAGE)

    expect(useChatStore.getState().chats.c1).toMatchObject({
      lastSeq: 7,
      lastMessage: expect.objectContaining({ text: 'hello' }),
    })
  })

  it('marks an incoming message as not ours', async () => {
    const harness = await bridge()

    await push(harness, MsgType.NEW, NEW_MESSAGE)

    expect(history(harness, 'c1')[0].outgoing).toBe(false)
  })

  /**
   * The gateway echoes a message back to its own sender, so this fires for
   * messages we sent too. Without the collapse the sender would see every one of
   * their own messages twice — once optimistically, once from fanout.
   */
  it('marks our own echoed message as outgoing', async () => {
    const harness = await bridge()

    await push(harness, MsgType.NEW, { ...NEW_MESSAGE, senderId: 'self' })

    expect(history(harness, 'c1')[0].outgoing).toBe(true)
  })

  it('does not duplicate a message that arrives twice', async () => {
    // Redelivery after a reconnect is normal; the message id is what collapses
    // the repeat onto the existing row.
    const harness = await bridge()

    await push(harness, MsgType.NEW, NEW_MESSAGE)
    await push(harness, MsgType.NEW, NEW_MESSAGE)

    expect(history(harness, 'c1')).toHaveLength(1)
  })

  it('keeps messages for different chats apart', async () => {
    const harness = await bridge()

    await push(harness, MsgType.NEW, NEW_MESSAGE)
    await push(harness, MsgType.NEW, { ...NEW_MESSAGE, messageId: 'm2', chatId: 'c2' })

    expect(history(harness, 'c1')).toHaveLength(1)
    expect(history(harness, 'c2')).toHaveLength(1)
  })

  it('carries an attachment through to the cache', async () => {
    const harness = await bridge()

    await push(harness, MsgType.NEW, {
      ...NEW_MESSAGE,
      attachment: {
        kind: 'image',
        mediaRef: 'media-1',
        filename: 'cat.png',
        mime: 'image/png',
        size: 10,
        width: 1,
        height: 1,
      },
    })

    expect(history(harness, 'c1')[0].attachment).toMatchObject({ mediaRef: 'media-1' })
  })
})

describe('read receipts', () => {
  it('records another member`s cursor', async () => {
    const harness = await bridge()

    await push(harness, MsgType.READ_UPD, { chatId: 'c1', userId: 'u2', upToChatSeq: 5 })

    expect(useReceiptStore.getState().cursors.c1.u2).toBe(5)
  })

  it('advances a cursor', async () => {
    const harness = await bridge()

    await push(harness, MsgType.READ_UPD, { chatId: 'c1', userId: 'u2', upToChatSeq: 5 })
    await push(harness, MsgType.READ_UPD, { chatId: 'c1', userId: 'u2', upToChatSeq: 9 })

    expect(useReceiptStore.getState().cursors.c1.u2).toBe(9)
  })

  it('never moves a cursor backwards on an out-of-order receipt', async () => {
    // READ_UPD is not ordered; a cursor that regressed would un-tick messages
    // the sender has already seen ticked.
    const harness = await bridge()

    await push(harness, MsgType.READ_UPD, { chatId: 'c1', userId: 'u2', upToChatSeq: 9 })
    await push(harness, MsgType.READ_UPD, { chatId: 'c1', userId: 'u2', upToChatSeq: 5 })

    expect(useReceiptStore.getState().cursors.c1.u2).toBe(9)
  })
})

describe('typing', () => {
  it('records someone typing', async () => {
    const harness = await bridge()

    await push(harness, MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })

    expect(useTypingStore.getState().typing.c1).toHaveProperty('u2')
  })

  it('clears it on an explicit stop', async () => {
    const harness = await bridge()

    await push(harness, MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })
    await push(harness, MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: false })

    expect(useTypingStore.getState().typing.c1).not.toHaveProperty('u2')
  })

  it('ignores our own typing echoed back', async () => {
    // Fanout sends it to every member including us; "you are typing" in your own
    // chat header is pure noise.
    const harness = await bridge()

    await push(harness, MsgType.TYPING, { chatId: 'c1', userId: 'self', active: true })

    expect(useTypingStore.getState().typing.c1 ?? {}).not.toHaveProperty('self')
  })

  /**
   * The gateway classifies TYPING as droppable and gives no "stopped" guarantee,
   * so an indicator whose stop frame was discarded would otherwise stay on
   * screen until something unrelated happened to arrive — which, in a quiet
   * chat, is never.
   */
  it('sweeps expired indicators on a timer', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useRealtimeSync())
    await harness.connect()

    await act(async () => {
      harness.socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })
      await flush()
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000)
    })

    expect(useTypingStore.getState().typing).toEqual({})
  })

  it('stops sweeping once unmounted', async () => {
    // A leaked interval keeps a dead tree writing to the store for the life of
    // the tab.
    vi.useFakeTimers()
    const clear = vi.spyOn(globalThis, 'clearInterval')
    const harness = await renderHookWithProvider(() => useRealtimeSync())

    harness.unmount()

    expect(clear).toHaveBeenCalled()
  })
})

describe('presence', () => {
  it('records a user coming online', async () => {
    const harness = await bridge()

    await push(harness, MsgType.PRESENCE, { userId: 'u2', online: true, lastSeenMs: 1_700 })

    expect(useUserDirectory.getState().users.u2).toMatchObject({
      online: true,
      lastSeenMs: 1_700,
    })
  })

  it('records a user going offline', async () => {
    const harness = await bridge()

    await push(harness, MsgType.PRESENCE, { userId: 'u2', online: true, lastSeenMs: 1 })
    await push(harness, MsgType.PRESENCE, { userId: 'u2', online: false, lastSeenMs: 2 })

    expect(useUserDirectory.getState().users.u2.online).toBe(false)
  })

  it('keeps a name the contact list supplied', async () => {
    // Presence carries no name; a wholesale replace would blank the label.
    useUserDirectory.getState().upsert({ userId: 'u2', name: 'Bob' })
    const harness = await bridge()

    await push(harness, MsgType.PRESENCE, { userId: 'u2', online: true, lastSeenMs: 1 })

    expect(useUserDirectory.getState().users.u2.name).toBe('Bob')
  })
})

describe('chat info', () => {
  it('records the chat kind, title and owner', async () => {
    // CHAT_INFO is the only moment the server tells this client a chat's type.
    const harness = await bridge()

    await push(harness, MsgType.CHAT_INFO, {
      chatId: 'c1',
      type: 'group',
      title: 'Team',
      ownerId: 'u9',
    })

    expect(useChatStore.getState().chats.c1).toMatchObject({
      kind: 'group',
      title: 'Team',
      ownerId: 'u9',
      provisional: false,
    })
  })

  it('falls back to the id when the chat has no title', async () => {
    // A 1:1 chat has no title; rendering an empty row would leave a blank line
    // in the chat list.
    const harness = await bridge()

    await push(harness, MsgType.CHAT_INFO, { chatId: 'c1', type: 'direct', title: '' })

    expect(useChatStore.getState().chats.c1.title).toBe('c1')
  })

  it('treats an unknown chat type as direct', async () => {
    // A newer gateway may name a kind this build has never heard of; falling
    // back keeps the chat usable rather than rendering nothing.
    const harness = await bridge()

    await push(harness, MsgType.CHAT_INFO, { chatId: 'c1', type: 'broadcast', title: 'X' })

    expect(useChatStore.getState().chats.c1.kind).toBe('direct')
  })
})

describe('reactions', () => {
  it('stores the tally the server sent', async () => {
    const harness = await bridge()

    await push(harness, MsgType.REACT_UPD, {
      chatId: 'c1',
      messageId: 'm1',
      userId: 'u2',
      emoji: '👍',
      added: true,
      counts: { '👍': 3 },
    })

    expect(useReactionStore.getState().byMessage.m1.counts).toEqual({ '👍': 3 })
  })

  it('marks the emoji as ours when we are the reactor', async () => {
    // The counts map says how many reacted, never who — this is the only place
    // "which one is mine" can be learned.
    const harness = await bridge()

    await push(harness, MsgType.REACT_UPD, {
      chatId: 'c1',
      messageId: 'm1',
      userId: 'self',
      emoji: '👍',
      added: true,
      counts: { '👍': 1 },
    })

    expect(useReactionStore.getState().byMessage.m1.mine).toBe('👍')
  })

  it('does not claim someone else`s reaction as ours', async () => {
    const harness = await bridge()

    await push(harness, MsgType.REACT_UPD, {
      chatId: 'c1',
      messageId: 'm1',
      userId: 'u2',
      emoji: '👍',
      added: true,
      counts: { '👍': 1 },
    })

    expect(useReactionStore.getState().byMessage.m1.mine).toBeNull()
  })

  it('tolerates an update with no counts map', async () => {
    // proto3 omits an empty map entirely, and the picker maps over it.
    const harness = await bridge()

    await push(harness, MsgType.REACT_UPD, {
      chatId: 'c1',
      messageId: 'm1',
      userId: 'u2',
      emoji: '👍',
      added: false,
    })

    expect(useReactionStore.getState().byMessage.m1.counts).toEqual({})
  })
})

describe('drafts', () => {
  it('merges a draft written on another device', async () => {
    const harness = await bridge()

    await push(harness, MsgType.DRAFTS, {
      drafts: [{ chatId: 'c1', text: 'unsent', replyTo: '', updatedAt: 2_000 }],
    })

    expect(useDraftStore.getState().byChat.c1).toMatchObject({ text: 'unsent' })
  })

  it('takes the newer of two drafts', async () => {
    const harness = await bridge()

    await push(harness, MsgType.DRAFTS, {
      drafts: [{ chatId: 'c1', text: 'old', replyTo: '', updatedAt: 1_000 }],
    })
    await push(harness, MsgType.DRAFTS, {
      drafts: [{ chatId: 'c1', text: 'new', replyTo: '', updatedAt: 2_000 }],
    })

    expect(useDraftStore.getState().byChat.c1.text).toBe('new')
  })

  it('clears a draft the other device emptied', async () => {
    const harness = await bridge()

    await push(harness, MsgType.DRAFTS, {
      drafts: [{ chatId: 'c1', text: 'something', replyTo: '', updatedAt: 1_000 }],
    })
    await push(harness, MsgType.DRAFTS, {
      drafts: [{ chatId: 'c1', text: '', replyTo: '', updatedAt: 2_000 }],
    })

    expect(useDraftStore.getState().byChat.c1).toBeUndefined()
  })

  it('carries the reply target', async () => {
    const harness = await bridge()

    await push(harness, MsgType.DRAFTS, {
      drafts: [{ chatId: 'c1', text: 'reply draft', replyTo: 'm7', updatedAt: 1_000 }],
    })

    expect(useDraftStore.getState().byChat.c1.replyTo).toBe('m7')
  })
})

describe('polls', () => {
  it('stores a poll tally', async () => {
    const harness = await bridge()

    await push(harness, MsgType.POLL_STATE, {
      pollId: 'p1',
      chatId: 'c1',
      messageId: 'm1',
      question: 'Lunch?',
      options: [{ index: 0, text: 'Pizza', votes: 2 }],
      totalVotes: 2,
    })

    expect(usePollStore.getState().byId.p1).toMatchObject({ question: 'Lunch?', totalVotes: 2 })
  })

  it('indexes the poll by its message', async () => {
    const harness = await bridge()

    await push(harness, MsgType.POLL_STATE, {
      pollId: 'p1',
      chatId: 'c1',
      messageId: 'm1',
      question: 'Lunch?',
    })

    expect(usePollStore.getState().byMessage.m1).toBe('p1')
  })
})

describe('profile mirroring', () => {
  /**
   * The gateway mirrors PROFILE_SET per user, so this only ever arrives for us.
   * The id check is belt-and-braces: acting on someone else's profile would
   * overwrite our own session with a stranger's name — silent corruption rather
   * than a visible failure.
   */
  it('updates our own session from a profile pushed by another device', async () => {
    const harness = await bridge()

    await push(harness, MsgType.PROFILE, {
      userId: 'self',
      username: 'alice',
      displayName: 'Alice Renamed',
      avatarRef: 'new-avatar',
    })

    expect(useSessionStore.getState().session).toMatchObject({
      displayName: 'Alice Renamed',
      avatarRef: 'new-avatar',
    })
  })

  it('ignores a profile for anyone else', async () => {
    const harness = await bridge()

    await push(harness, MsgType.PROFILE, {
      userId: 'u2',
      username: 'bob',
      displayName: 'Bob',
      avatarRef: 'bob-avatar',
    })

    // Nothing of Bob's may reach our session.
    expect(useSessionStore.getState().session).toMatchObject({ username: 'alice' })
    expect(useSessionStore.getState().session?.displayName).not.toBe('Bob')
    expect(useSessionStore.getState().session?.avatarRef).not.toBe('bob-avatar')
  })

  it('caches the profile under both the "me" key and our id', async () => {
    // The profile panel queries by "" for our own account and by id elsewhere;
    // populating only one leaves the other fetching on next open.
    const harness = await bridge()

    await push(harness, MsgType.PROFILE, {
      userId: 'self',
      username: 'alice',
      displayName: 'Alice',
      avatarRef: '',
    })

    expect(harness.queryClient.getQueryData(queryKeys.profile(''))).toMatchObject({
      userId: 'self',
    })
    expect(harness.queryClient.getQueryData(queryKeys.profile('self'))).toMatchObject({
      userId: 'self',
    })
  })
})

describe('pins', () => {
  it('invalidates the pin query rather than patching it', async () => {
    // The push carries the whole new set and the query owns that data, so a
    // refetch is both simpler and impossible to get out of step.
    const harness = await bridge()
    const invalidate = vi.spyOn(harness.queryClient, 'invalidateQueries')

    await push(harness, MsgType.PINNED, { chatId: 'c1', pins: [] })

    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.pins('c1') })
  })
})

describe('lifecycle', () => {
  it('subscribes nothing until there is a session', async () => {
    // Routing a message with no `selfId` would file every bubble as incoming,
    // including our own.
    useSessionStore.setState({ status: 'anonymous', session: null })
    const harness = await renderHookWithProvider(() => useRealtimeSync())
    await harness.connect()

    await push(harness, MsgType.NEW, NEW_MESSAGE)

    expect(history(harness, 'c1')).toHaveLength(0)
  })

  it('stops routing after unmount', async () => {
    // Every subscription returns an unsubscribe; leaking one would keep a dead
    // tree writing into the stores.
    const harness = await bridge()
    harness.unmount()

    await act(async () => {
      harness.socket.deliver(MsgType.NEW, NEW_MESSAGE)
      await flush()
    })

    expect(useChatStore.getState().chats.c1).toBeUndefined()
  })

  it('logs an uncorrelated error without disturbing state', async () => {
    // Anything tied to a request already rejected that request's promise, so
    // there is nothing left to do but record it.
    const harness = await bridge()

    await push(harness, MsgType.ERROR, { code: ErrorCode.INTERNAL, message: 'boom' })

    expect(console.warn).toHaveBeenCalled()
    expect(harness.client.state).toBe('ready')
  })

  it('survives an unknown push type', async () => {
    const harness = await bridge()

    await act(async () => {
      harness.socket.deliver(210, null)
      await flush()
    })

    expect(harness.client.state).toBe('ready')
  })
})
