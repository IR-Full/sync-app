import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { config } from '@/shared/config/env'
import { selectTypingUserIds, useTypingStore } from './typing'

const store = () => useTypingStore.getState()

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-08-06T12:00:00Z'))
  store().clear()
})

afterEach(() => vi.useRealTimers())

describe('mark', () => {
  it('records a typing user', () => {
    store().mark('c1', 'u2', true)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual(['u2'])
  })

  it('removes a user on active=false', () => {
    store().mark('c1', 'u2', true)
    store().mark('c1', 'u2', false)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual([])
  })

  it('tolerates active=false for someone who was not typing', () => {
    // TYPING frames are droppable, so a stop can arrive without its start.
    expect(() => store().mark('c1', 'u2', false)).not.toThrow()
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual([])
  })

  it('tracks several users in one chat', () => {
    store().mark('c1', 'u2', true)
    store().mark('c1', 'u3', true)
    expect(selectTypingUserIds(store(), 'c1', 'u1').sort()).toEqual(['u2', 'u3'])
  })

  it('keeps chats separate', () => {
    store().mark('c1', 'u2', true)
    expect(selectTypingUserIds(store(), 'c2', 'u1')).toEqual([])
  })

  /**
   * Each indicator carries its own expiry because the protocol gives no
   * guarantee that a stop frame arrives at all — the gateway throttles TYPING to
   * roughly one per chat every two seconds and drops the rest. Without an
   * expiry, one dropped stop leaves "…is typing" on screen forever.
   */
  it('refreshes the expiry on a repeated start', () => {
    store().mark('c1', 'u2', true)
    vi.advanceTimersByTime(config.typingTimeoutMs - 500)
    store().mark('c1', 'u2', true)

    vi.advanceTimersByTime(config.typingTimeoutMs - 500)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual(['u2'])
  })
})

describe('expiry', () => {
  it('hides an indicator once its timeout passes', () => {
    store().mark('c1', 'u2', true)
    vi.advanceTimersByTime(config.typingTimeoutMs + 1)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual([])
  })

  it('still shows an indicator just before it expires', () => {
    store().mark('c1', 'u2', true)
    vi.advanceTimersByTime(config.typingTimeoutMs - 1)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual(['u2'])
  })

  it('expires users independently', () => {
    store().mark('c1', 'u2', true)
    vi.advanceTimersByTime(1000)
    store().mark('c1', 'u3', true)

    vi.advanceTimersByTime(config.typingTimeoutMs - 500)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual(['u3'])
  })
})

describe('sweep', () => {
  it('drops expired entries from the store, not just from the view', () => {
    // The selector filters by expiry, so the UI is already correct. The sweep
    // exists so a long session does not accumulate a row per user per chat.
    store().mark('c1', 'u2', true)
    vi.advanceTimersByTime(config.typingTimeoutMs + 1)
    store().sweep()

    expect(store().typing).toEqual({})
  })

  it('keeps live entries', () => {
    store().mark('c1', 'u2', true)
    store().sweep()
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual(['u2'])
  })

  it('drops a chat once its last typist expires', () => {
    store().mark('c1', 'u2', true)
    store().mark('c2', 'u3', true)
    vi.advanceTimersByTime(config.typingTimeoutMs + 1)
    store().mark('c2', 'u3', true)
    store().sweep()

    expect(Object.keys(store().typing)).toEqual(['c2'])
  })

  it('returns the same state object when nothing expired', () => {
    // The sweep runs on an interval. Producing a fresh object every tick would
    // re-render every chat header a few times a second for no reason.
    store().mark('c1', 'u2', true)
    const before = store().typing
    store().sweep()
    expect(store().typing).toBe(before)
  })

  it('is a no-op on an empty store', () => {
    expect(() => store().sweep()).not.toThrow()
    expect(store().typing).toEqual({})
  })
})

describe('selectTypingUserIds', () => {
  it('excludes ourselves', () => {
    // Our own TYPING is echoed back by fanout; showing "you are typing" in your
    // own chat header is pure noise.
    store().mark('c1', 'u1', true)
    store().mark('c1', 'u2', true)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toEqual(['u2'])
  })

  it('returns a stable empty array for an unknown chat', () => {
    // A fresh `[]` each call would break referential equality in the selector and
    // re-render every chat header on every store update.
    const first = selectTypingUserIds(store(), 'unknown', 'u1')
    const second = selectTypingUserIds(store(), 'other', 'u1')
    expect(first).toBe(second)
  })

  it('returns a stable empty array when everyone has expired', () => {
    store().mark('c1', 'u2', true)
    vi.advanceTimersByTime(config.typingTimeoutMs + 1)
    expect(selectTypingUserIds(store(), 'c1', 'u1')).toBe(
      selectTypingUserIds(store(), 'other', 'u1'),
    )
  })
})

describe('clear', () => {
  it('empties every chat', () => {
    store().mark('c1', 'u2', true)
    store().mark('c2', 'u3', true)
    store().clear()
    expect(store().typing).toEqual({})
  })
})
