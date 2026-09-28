import { beforeEach, describe, expect, it } from 'vitest'

import type { Wire } from '@/shared/api'
import { chatKindFromString, selectOrderedChats, useChatStore } from './store'
import { isDirect, unreadCount, type ChatSummary } from './types'

const SELF = 'me'

function reset() {
  useChatStore.getState().reset()
  localStorage.clear()
  useChatStore.getState().load(SELF)
}

/** A NEW frame as the gateway decodes it — proto3 defaults included. */
function inbound(over: Partial<Wire.NewMessage> = {}): Wire.NewMessage {
  return {
    messageId: 'm1',
    chatId: 'c1',
    senderId: 'them',
    chatSeq: 1,
    text: 'hello',
    mediaRef: '',
    replyTo: '',
    edited: false,
    deleted: false,
    timestamp: 1000,
    threadRoot: '',
    replyCount: 0,
    expiresAt: 0,
    ...over,
  } as Wire.NewMessage
}

beforeEach(reset)

describe('upsert', () => {
  it('creates a chat and merges later patches', () => {
    const { upsert } = useChatStore.getState()
    upsert({ id: 'c1', kind: 'group', title: 'Team' })
    upsert({ id: 'c1', lastSeq: 5 })

    const chat = useChatStore.getState().chats.c1
    expect(chat.kind).toBe('group')
    expect(chat.title).toBe('Team') // the earlier patch survives the later one
    expect(chat.lastSeq).toBe(5)
  })

  /**
   * A chat that has seen real traffic must never be demoted back to a
   * placeholder by a later contact sync — otherwise a conversation with history
   * reappears in the UI as "not started yet".
   */
  it('never demotes a real chat back to provisional', () => {
    const { upsert } = useChatStore.getState()
    upsert({ id: 'c1', provisional: false })
    upsert({ id: 'c1', provisional: true })

    expect(useChatStore.getState().chats.c1.provisional).toBe(false)
  })

  it('keeps a placeholder provisional until something proves otherwise', () => {
    useChatStore.getState().upsert({ id: 'c1', provisional: true })
    expect(useChatStore.getState().chats.c1.provisional).toBe(true)
  })
})

describe('applyMessage', () => {
  it('creates the chat a message implies', () => {
    useChatStore.getState().applyMessage(inbound(), SELF)

    const chat = useChatStore.getState().chats.c1
    expect(chat.lastSeq).toBe(1)
    expect(chat.lastMessage?.text).toBe('hello')
    // A message proves the chat exists, so it is no longer a placeholder.
    expect(chat.provisional).toBe(false)
  })

  it('learns the peer of a direct chat from the first inbound message', () => {
    // No message reports chat membership, so the sender of an inbound message is
    // the only place a 1:1 peer id can come from — presence and secret chats
    // both need it.
    useChatStore.getState().applyMessage(inbound({ senderId: 'bob' }), SELF)
    expect(useChatStore.getState().chats.c1.peerUserId).toBe('bob')
  })

  it('does not mistake our own message for the peer', () => {
    useChatStore.getState().applyMessage(inbound({ senderId: SELF }), SELF)
    expect(useChatStore.getState().chats.c1.peerUserId).toBeUndefined()
  })

  it('treats our own message as read', () => {
    useChatStore.getState().applyMessage(inbound({ senderId: SELF, chatSeq: 4 }), SELF)

    const chat = useChatStore.getState().chats.c1
    expect(chat.lastReadSeq).toBe(4)
    expect(unreadCount(chat)).toBe(0)
  })

  it("leaves the read cursor alone for someone else's message", () => {
    useChatStore.getState().applyMessage(inbound({ senderId: 'them', chatSeq: 3 }), SELF)

    const chat = useChatStore.getState().chats.c1
    expect(chat.lastReadSeq).toBe(0)
    expect(unreadCount(chat)).toBe(3)
  })

  /**
   * History backfill replays OLDER messages through this same path. Without the
   * sequence guard, opening a chat and scrolling up would rewrite the list
   * preview with a message from last week.
   */
  it('does not let a backfilled older message overwrite the preview', () => {
    const { applyMessage } = useChatStore.getState()
    applyMessage(
      inbound({ messageId: 'new', chatSeq: 10, text: 'latest', timestamp: 5000 }),
      SELF,
    )
    applyMessage(
      inbound({ messageId: 'old', chatSeq: 2, text: 'ancient', timestamp: 1000 }),
      SELF,
    )

    const chat = useChatStore.getState().chats.c1
    expect(chat.lastMessage?.text).toBe('latest')
    expect(chat.lastSeq).toBe(10)
    expect(chat.updatedAt).toBe(5000)
  })

  it('advances the preview for a newer message', () => {
    const { applyMessage } = useChatStore.getState()
    applyMessage(inbound({ chatSeq: 1, text: 'first', timestamp: 1000 }), SELF)
    applyMessage(
      inbound({ messageId: 'm2', chatSeq: 2, text: 'second', timestamp: 2000 }),
      SELF,
    )

    expect(useChatStore.getState().chats.c1.lastMessage?.text).toBe('second')
  })

  it('carries a deletion through to the preview', () => {
    useChatStore.getState().applyMessage(inbound({ deleted: true, text: '' }), SELF)
    expect(useChatStore.getState().chats.c1.lastMessage?.deleted).toBe(true)
  })
})

describe('markRead', () => {
  it('advances the read cursor', () => {
    const store = useChatStore.getState()
    store.applyMessage(inbound({ chatSeq: 9 }), SELF)
    store.markRead('c1', 9)

    expect(unreadCount(useChatStore.getState().chats.c1)).toBe(0)
  })

  /**
   * A read cursor only moves forward. Receipts arrive out of order across
   * devices, and a cursor that could go backwards would resurrect a badge the
   * user already cleared.
   */
  it('never moves the cursor backwards', () => {
    const store = useChatStore.getState()
    store.applyMessage(inbound({ chatSeq: 9 }), SELF)
    store.markRead('c1', 9)
    store.markRead('c1', 3)

    expect(useChatStore.getState().chats.c1.lastReadSeq).toBe(9)
  })

  it('ignores a chat it does not know', () => {
    useChatStore.getState().markRead('never-seen', 5) // must not create or throw
    expect(useChatStore.getState().chats['never-seen']).toBeUndefined()
  })
})

describe('persistence', () => {
  it('reloads a chat registry for the same account', () => {
    useChatStore.getState().upsert({ id: 'c1', title: 'Persisted' })

    useChatStore.getState().reset()
    expect(useChatStore.getState().chats.c1).toBeUndefined()

    useChatStore.getState().load(SELF)
    expect(useChatStore.getState().chats.c1?.title).toBe('Persisted')
  })

  /**
   * The registry is keyed per account. A messenger that showed the previous
   * user's chat list after a second account signs in has leaked it.
   */
  it('does not leak one account registry into another', () => {
    useChatStore.getState().upsert({ id: 'c1', title: 'Mine' })

    useChatStore.getState().load('someone-else')
    expect(useChatStore.getState().chats.c1).toBeUndefined()

    useChatStore.getState().load(SELF)
    expect(useChatStore.getState().chats.c1?.title).toBe('Mine')
  })
})

describe('remove', () => {
  it('drops a chat', () => {
    useChatStore.getState().upsert({ id: 'c1' })
    useChatStore.getState().remove('c1')
    expect(useChatStore.getState().chats.c1).toBeUndefined()
  })
})

describe('selectOrderedChats', () => {
  it('orders by most recent activity', () => {
    const { upsert } = useChatStore.getState()
    upsert({ id: 'old', updatedAt: 1000 })
    upsert({ id: 'newest', updatedAt: 3000 })
    upsert({ id: 'middle', updatedAt: 2000 })

    const ordered = selectOrderedChats(useChatStore.getState()).map((c) => c.id)
    expect(ordered).toEqual(['newest', 'middle', 'old'])
  })

  it('returns an empty list for an empty registry', () => {
    expect(selectOrderedChats(useChatStore.getState())).toEqual([])
  })
})

describe('unreadCount', () => {
  it('derives from the two server-assigned sequences', () => {
    expect(unreadCount({ lastSeq: 10, lastReadSeq: 4 } as ChatSummary)).toBe(6)
    expect(unreadCount({ lastSeq: 4, lastReadSeq: 4 } as ChatSummary)).toBe(0)
  })

  it('never reports a negative count', () => {
    // A read cursor ahead of lastSeq happens transiently: a read receipt from
    // another device can arrive before the message it refers to.
    expect(unreadCount({ lastSeq: 2, lastReadSeq: 9 } as ChatSummary)).toBe(0)
  })
})

describe('chatKindFromString', () => {
  it('maps the kinds the server sends', () => {
    expect(chatKindFromString('group')).toBe('group')
    expect(chatKindFromString('channel')).toBe('channel')
    expect(chatKindFromString('direct')).toBe('direct')
  })

  it('falls back to direct for anything unrecognised', () => {
    // A newer gateway could add a kind; defaulting to direct keeps the row
    // renderable rather than crashing the list.
    expect(chatKindFromString('supergroup')).toBe('direct')
    expect(chatKindFromString('')).toBe('direct')
  })
})

describe('isDirect', () => {
  it('identifies 1:1 chats', () => {
    expect(isDirect({ kind: 'direct' } as ChatSummary)).toBe(true)
    expect(isDirect({ kind: 'group' } as ChatSummary)).toBe(false)
  })
})
