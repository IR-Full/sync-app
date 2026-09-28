import { beforeEach, describe, expect, it } from 'vitest'

import { QUICK_REACTIONS, selectReactions, useReactionStore } from './reactions'

const store = () => useReactionStore.getState()

beforeEach(() => store().clear())

describe('apply', () => {
  /**
   * REACT_UPD carries the *post-change* counts, so the tally is replaced rather
   * than accumulated. Incrementing locally would double-count our own reaction:
   * the optimistic bump plus the server's already-updated map.
   */
  it('takes the server counts wholesale', () => {
    store().apply('m1', { '👍': 3 }, '👍', true, false)
    expect(selectReactions(store(), 'm1').counts).toEqual({ '👍': 3 })
  })

  it('replaces an earlier tally rather than merging into it', () => {
    // A reaction removed by someone else disappears from the counts map
    // entirely; merging would leave the stale emoji on screen forever.
    store().apply('m1', { '👍': 3, '❤️': 1 }, '👍', true, false)
    store().apply('m1', { '👍': 2 }, '👍', false, false)
    expect(selectReactions(store(), 'm1').counts).toEqual({ '👍': 2 })
  })

  it('records our own emoji when we add one', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, true)
    expect(selectReactions(store(), 'm1').mine).toBe('👍')
  })

  it('clears our own emoji when we remove it', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, true)
    store().apply('m1', {}, '👍', false, true)
    expect(selectReactions(store(), 'm1').mine).toBeNull()
  })

  it('switches our emoji when we pick a different one', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, true)
    store().apply('m1', { '❤️': 1 }, '❤️', true, true)
    expect(selectReactions(store(), 'm1').mine).toBe('❤️')
  })

  /**
   * The counts map says how many people reacted, never who. So "which one is
   * mine" is the single piece of state that cannot be recovered from the server
   * payload — and losing it un-highlights the user's own button.
   */
  it('keeps our emoji when someone else reacts', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, true)
    store().apply('m1', { '👍': 1, '😂': 1 }, '😂', true, false)
    expect(selectReactions(store(), 'm1').mine).toBe('👍')
  })

  it('keeps our emoji when someone else removes theirs', () => {
    store().apply('m1', { '👍': 2 }, '👍', true, true)
    store().apply('m1', { '👍': 1 }, '👍', false, false)
    expect(selectReactions(store(), 'm1').mine).toBe('👍')
  })

  it('leaves our emoji unset when the first update is someone else`s', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, false)
    expect(selectReactions(store(), 'm1').mine).toBeNull()
  })

  it('keeps messages independent', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, true)
    store().apply('m2', { '❤️': 5 }, '❤️', true, false)

    expect(selectReactions(store(), 'm1')).toEqual({ counts: { '👍': 1 }, mine: '👍' })
    expect(selectReactions(store(), 'm2')).toEqual({ counts: { '❤️': 5 }, mine: null })
  })

  it('accepts an empty counts map when the last reaction is removed', () => {
    store().apply('m1', {}, '👍', false, true)
    expect(selectReactions(store(), 'm1').counts).toEqual({})
  })
})

describe('selectReactions', () => {
  it('returns an empty tally for a message with no reactions', () => {
    expect(selectReactions(store(), 'never-reacted')).toEqual({ counts: {}, mine: null })
  })

  it('returns a stable empty tally', () => {
    // History frames carry no reaction data, so most messages hit this path. A
    // fresh object each call would re-render every bubble on every store change.
    expect(selectReactions(store(), 'a')).toBe(selectReactions(store(), 'b'))
  })
})

describe('QUICK_REACTIONS', () => {
  it('offers a non-empty picker', () => {
    expect(QUICK_REACTIONS.length).toBeGreaterThan(0)
  })

  it('lists no duplicates', () => {
    // A duplicate would render two identical buttons and give React two children
    // with the same key.
    expect(new Set(QUICK_REACTIONS).size).toBe(QUICK_REACTIONS.length)
  })

  it('stays short enough to fit one row on a phone', () => {
    expect(QUICK_REACTIONS.length).toBeLessThanOrEqual(8)
  })
})

describe('clear', () => {
  it('drops every tally', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, true)
    store().clear()
    expect(store().byMessage).toEqual({})
  })

  it('forgets which emoji was ours, so a new account starts clean', () => {
    store().apply('m1', { '👍': 1 }, '👍', true, true)
    store().clear()
    expect(selectReactions(store(), 'm1').mine).toBeNull()
  })
})
