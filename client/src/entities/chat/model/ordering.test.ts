import { describe, expect, it } from 'vitest'

import {
  chatActivity,
  compareChats,
  is1to1,
  isArchived,
  isMuted,
  isPinned,
  isSecret,
  type ChatSummary,
} from './types'

function chat(patch: Partial<ChatSummary> & { id: string }): ChatSummary {
  return {
    kind: 'direct',
    title: patch.id,
    lastSeq: 0,
    lastReadSeq: 0,
    updatedAt: 0,
    ...patch,
  }
}

describe('chatActivity', () => {
  /**
   * Neither timestamp is right alone, which is the whole reason there are two.
   * `updatedAt` is when THIS client last touched the row, so a chat that was busy
   * while the tab was closed has none; `lastActivityAt` is the server's, so it knows
   * nothing about a draft typed a second ago.
   */
  it('takes the later of the local and server timestamps', () => {
    expect(chatActivity(chat({ id: 'a', updatedAt: 100, lastActivityAt: 500 }))).toBe(500)
    expect(chatActivity(chat({ id: 'b', updatedAt: 900, lastActivityAt: 500 }))).toBe(900)
  })

  it('works when the server has never reported one', () => {
    expect(chatActivity(chat({ id: 'a', updatedAt: 42 }))).toBe(42)
  })
})

describe('compareChats', () => {
  /**
   * Sorting by `updatedAt` alone — which is what this did before the server sent an
   * activity timestamp — put a chat that received forty messages while the tab was
   * closed at the BOTTOM of the list, because this client had not touched it.
   */
  it('orders by activity, newest first', () => {
    const rows = [
      chat({ id: '1', lastActivityAt: 100 }),
      chat({ id: '2', lastActivityAt: 300 }),
      chat({ id: '3', lastActivityAt: 200 }),
    ]
    expect([...rows].sort(compareChats).map((c) => c.id)).toEqual(['2', '3', '1'])
  })

  it('puts pinned chats first regardless of activity', () => {
    const rows = [
      chat({ id: 'busy', lastActivityAt: 9999 }),
      chat({ id: 'pinned', lastActivityAt: 1, flags: { pinned: true } }),
    ]
    expect([...rows].sort(compareChats).map((c) => c.id)).toEqual(['pinned', 'busy'])
  })

  it('orders pinned chats among themselves by activity', () => {
    const rows = [
      chat({ id: 'old', lastActivityAt: 10, flags: { pinned: true } }),
      chat({ id: 'new', lastActivityAt: 20, flags: { pinned: true } }),
    ]
    expect([...rows].sort(compareChats).map((c) => c.id)).toEqual(['new', 'old'])
  })

  /**
   * A fresh install has every activity at zero, and a comparator that returned 0 for
   * all of them would let the order shuffle between renders as the underlying object
   * order changed.
   */
  it('is stable for chats with no activity at all', () => {
    const rows = [chat({ id: '1' }), chat({ id: '2' }), chat({ id: '3' })]
    const first = [...rows].sort(compareChats).map((c) => c.id)
    const second = [...rows].reverse().sort(compareChats).map((c) => c.id)
    expect(first).toEqual(second)
  })
})

describe('isMuted', () => {
  /**
   * A DEADLINE, not a flag. "Mute for eight hours" is what muting usually means and a
   * boolean cannot express it — so the check is against a time, and an expired
   * deadline is simply not muted.
   */
  it('is true only until the deadline passes', () => {
    const c = chat({ id: 'a', flags: { mutedUntil: 1_000 } })
    expect(isMuted(c, 500)).toBe(true)
    expect(isMuted(c, 1_000)).toBe(false)
    expect(isMuted(c, 2_000)).toBe(false)
  })

  it('is false with no flags at all', () => {
    expect(isMuted(chat({ id: 'a' }), 1)).toBe(false)
    expect(isMuted(chat({ id: 'b', flags: {} }), 1)).toBe(false)
    expect(isMuted(chat({ id: 'c', flags: { mutedUntil: 0 } }), 1)).toBe(false)
  })
})

describe('flag predicates', () => {
  it('read pinned and archived', () => {
    expect(isPinned(chat({ id: 'a', flags: { pinned: true } }))).toBe(true)
    expect(isPinned(chat({ id: 'b' }))).toBe(false)
    expect(isArchived(chat({ id: 'c', flags: { archived: true } }))).toBe(true)
    expect(isArchived(chat({ id: 'd' }))).toBe(false)
  })
})

describe('is1to1', () => {
  /**
   * Both direct and secret chats are two-party and have no title of their own, so the
   * row is named after its peer. A check written as `kind === 'direct'` is exactly the
   * one that silently omits the type added later — which is a row rendering with a
   * blank name.
   */
  it('covers direct AND secret', () => {
    expect(is1to1(chat({ id: 'a', kind: 'direct' }))).toBe(true)
    expect(is1to1(chat({ id: 'b', kind: 'secret' }))).toBe(true)
    expect(is1to1(chat({ id: 'c', kind: 'group' }))).toBe(false)
    expect(is1to1(chat({ id: 'd', kind: 'channel' }))).toBe(false)
  })
})

describe('isSecret', () => {
  it('identifies the secret type', () => {
    expect(isSecret(chat({ id: 'a', kind: 'secret' }))).toBe(true)
    expect(isSecret(chat({ id: 'b', kind: 'direct' }))).toBe(false)
  })
})
