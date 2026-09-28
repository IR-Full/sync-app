import { describe, expect, it } from 'vitest'

import { queryKeys } from './query-keys'

/**
 * These keys are the contract between a query and the mutation that invalidates
 * it. A drift between the two is invisible — the query just never refetches —
 * so the properties worth pinning are: same input means the same key, and
 * different inputs never collide.
 */
describe('queryKeys', () => {
  it('is deterministic for the same input', () => {
    expect(queryKeys.history('chat-1')).toEqual(queryKeys.history('chat-1'))
    expect(queryKeys.profile('@bob')).toEqual(queryKeys.profile('@bob'))
  })

  it('separates chats within one key family', () => {
    expect(queryKeys.history('chat-1')).not.toEqual(queryKeys.history('chat-2'))
    expect(queryKeys.pins('chat-1')).not.toEqual(queryKeys.pins('chat-2'))
  })

  it('never collides across key families', () => {
    // `history` and `pins` are both keyed by chat id. If they shared a prefix,
    // invalidating pins after a pin/unpin would also blow away the message
    // history cache and re-request every page the user had scrolled through.
    const all = [
      queryKeys.history('x'),
      queryKeys.pins('x'),
      queryKeys.search('x'),
      queryKeys.profile('x'),
      queryKeys.contacts(),
      queryKeys.drafts(),
    ]
    const serialised = all.map((key) => JSON.stringify(key))
    expect(new Set(serialised).size).toBe(all.length)
  })

  it('puts the family name first so a prefix match scopes to one family', () => {
    // TanStack matches keys by prefix; `['history']` must invalidate every chat's
    // history and nothing else.
    expect(queryKeys.history('c')[0]).toBe('history')
    expect(queryKeys.pins('c')[0]).toBe('pins')
    expect(queryKeys.search('q')[0]).toBe('search')
    expect(queryKeys.profile('u')[0]).toBe('profile')
  })

  it('keys our own profile distinctly from a named one', () => {
    // "" means "me" — it has to stay a separate cache entry from any real user.
    expect(queryKeys.profile('')).not.toEqual(queryKeys.profile('@me'))
  })

  it('gives collection queries a constant key', () => {
    expect(queryKeys.contacts()).toEqual(queryKeys.contacts())
    expect(queryKeys.drafts()).toEqual(queryKeys.drafts())
  })
})
