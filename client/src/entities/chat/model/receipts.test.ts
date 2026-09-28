import { beforeEach, describe, expect, it } from 'vitest'

import { selectPeerReadSeq, useReceiptStore } from './receipts'

const read = (chatId: string, userId: string, seq: number) =>
  useReceiptStore.getState().apply(chatId, userId, seq)

beforeEach(() => useReceiptStore.getState().clear())

describe('apply', () => {
  it('records a cursor', () => {
    read('c1', 'u2', 5)
    expect(useReceiptStore.getState().cursors.c1.u2).toBe(5)
  })

  it('advances a cursor', () => {
    read('c1', 'u2', 5)
    read('c1', 'u2', 9)
    expect(useReceiptStore.getState().cursors.c1.u2).toBe(9)
  })

  /**
   * READ_UPD is not ordered. A cursor that moved backwards would un-tick
   * messages the sender has already seen ticked — a visible flicker that reads
   * as data loss.
   */
  it('never moves a cursor backwards', () => {
    read('c1', 'u2', 9)
    read('c1', 'u2', 5)
    expect(useReceiptStore.getState().cursors.c1.u2).toBe(9)
  })

  it('ignores a repeat of the current cursor', () => {
    read('c1', 'u2', 9)
    const before = useReceiptStore.getState().cursors
    read('c1', 'u2', 9)
    // Identity, not equality: every subscribed component re-renders on a new
    // object, and duplicate receipts are common under fanout.
    expect(useReceiptStore.getState().cursors).toBe(before)
  })

  it('tracks members of one chat independently', () => {
    read('c1', 'u2', 5)
    read('c1', 'u3', 8)
    expect(useReceiptStore.getState().cursors.c1).toEqual({ u2: 5, u3: 8 })
  })

  it('keeps chats separate', () => {
    read('c1', 'u2', 5)
    read('c2', 'u2', 12)
    expect(useReceiptStore.getState().cursors.c1.u2).toBe(5)
    expect(useReceiptStore.getState().cursors.c2.u2).toBe(12)
  })
})

describe('selectPeerReadSeq', () => {
  /**
   * Drives the read tick on our own messages: a tick appears once *someone else*
   * has read up to that sequence. Counting our own cursor would tick every
   * message the moment we opened the chat.
   */
  it('returns 0 for a chat with no receipts', () => {
    expect(selectPeerReadSeq(useReceiptStore.getState(), 'unknown', 'u1')).toBe(0)
  })

  it('returns the only peer cursor', () => {
    read('c1', 'u2', 7)
    expect(selectPeerReadSeq(useReceiptStore.getState(), 'c1', 'u1')).toBe(7)
  })

  it('returns the highest cursor in a group', () => {
    // One member having read further is enough to tick — the UI shows "read",
    // not "read by everyone".
    read('c1', 'u2', 7)
    read('c1', 'u3', 12)
    read('c1', 'u4', 3)
    expect(selectPeerReadSeq(useReceiptStore.getState(), 'c1', 'u1')).toBe(12)
  })

  it('excludes our own cursor', () => {
    read('c1', 'u1', 99)
    read('c1', 'u2', 4)
    expect(selectPeerReadSeq(useReceiptStore.getState(), 'c1', 'u1')).toBe(4)
  })

  it('returns 0 when only our own cursor is present', () => {
    read('c1', 'u1', 99)
    expect(selectPeerReadSeq(useReceiptStore.getState(), 'c1', 'u1')).toBe(0)
  })

  it('does not leak a cursor from another chat', () => {
    read('c2', 'u2', 50)
    expect(selectPeerReadSeq(useReceiptStore.getState(), 'c1', 'u1')).toBe(0)
  })
})

describe('clear', () => {
  it('drops every cursor', () => {
    read('c1', 'u2', 5)
    read('c2', 'u3', 8)
    useReceiptStore.getState().clear()
    expect(useReceiptStore.getState().cursors).toEqual({})
  })

  it('is what logout relies on, so nothing survives into the next session', () => {
    // These cursors are another account's data; leaving them would show one
    // user's read state on another user's messages after a switch.
    read('c1', 'u2', 5)
    useReceiptStore.getState().clear()
    expect(selectPeerReadSeq(useReceiptStore.getState(), 'c1', 'u1')).toBe(0)
  })
})
