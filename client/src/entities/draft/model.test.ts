import { beforeEach, describe, expect, it } from 'vitest'

import { useDraftStore, type Draft } from './model'

const draft = (overrides: Partial<Draft> = {}): Draft => ({
  chatId: 'c1',
  text: 'unsent thought',
  replyTo: '',
  updatedAt: 1_000,
  ...overrides,
})

const store = () => useDraftStore.getState()

beforeEach(() => store().clear())

describe('merge', () => {
  it('stores a draft', () => {
    store().merge([draft()])
    expect(store().byChat.c1).toMatchObject({ text: 'unsent thought' })
  })

  it('stores several chats at once', () => {
    store().merge([draft({ chatId: 'c1' }), draft({ chatId: 'c2', text: 'other' })])
    expect(Object.keys(store().byChat).sort()).toEqual(['c1', 'c2'])
  })

  /**
   * DRAFT_SET gets no reply; the server instead mirrors a DRAFTS frame to this
   * user's *other* devices. Two devices editing the same draft therefore race,
   * and the server timestamp is the only clock both agree on — a local
   * `Date.now()` comparison would resolve differently on each device.
   */
  it('takes the newer draft', () => {
    store().merge([draft({ text: 'old', updatedAt: 1_000 })])
    store().merge([draft({ text: 'new', updatedAt: 2_000 })])
    expect(store().byChat.c1.text).toBe('new')
  })

  it('ignores an older draft arriving late', () => {
    // Frames from two devices can arrive out of order after a reconnect.
    store().merge([draft({ text: 'new', updatedAt: 2_000 })])
    store().merge([draft({ text: 'old', updatedAt: 1_000 })])
    expect(store().byChat.c1.text).toBe('new')
  })

  it('accepts a draft with the same timestamp', () => {
    // Not `>=` on the incoming side: equal stamps mean a re-send of the same
    // state, and taking it is harmless while dropping it could lose a real edit
    // whose stamp collided at millisecond resolution.
    store().merge([draft({ text: 'first', updatedAt: 1_000 })])
    store().merge([draft({ text: 'second', updatedAt: 1_000 })])
    expect(store().byChat.c1.text).toBe('second')
  })

  it('deletes the draft when the text is cleared', () => {
    // Clearing the composer on another device has to clear it here too — an
    // empty draft is the absence of one, not a draft containing "".
    store().merge([draft({ text: 'something' })])
    store().merge([draft({ text: '', updatedAt: 2_000 })])

    expect(store().byChat.c1).toBeUndefined()
  })

  it('does not delete a draft when an older clear arrives', () => {
    store().merge([draft({ text: 'current', updatedAt: 2_000 })])
    store().merge([draft({ text: '', updatedAt: 1_000 })])
    expect(store().byChat.c1.text).toBe('current')
  })

  it('tolerates a clear for a chat with no draft', () => {
    expect(() => store().merge([draft({ text: '' })])).not.toThrow()
    expect(store().byChat.c1).toBeUndefined()
  })

  it('keeps the reply target alongside the text', () => {
    // A draft composed as a reply must come back as a reply on the other device.
    store().merge([draft({ replyTo: 'm7' })])
    expect(store().byChat.c1.replyTo).toBe('m7')
  })

  it('accepts an empty batch', () => {
    store().merge([draft()])
    store().merge([])
    expect(store().byChat.c1.text).toBe('unsent thought')
  })

  it('leaves other chats alone', () => {
    store().merge([draft({ chatId: 'c1' }), draft({ chatId: 'c2', text: 'keep me' })])
    store().merge([draft({ chatId: 'c1', text: '', updatedAt: 2_000 })])

    expect(store().byChat.c2.text).toBe('keep me')
  })

  it('applies a whole sync batch in one pass', () => {
    store().merge([draft({ chatId: 'c1', text: 'old', updatedAt: 1_000 })])
    store().merge([
      draft({ chatId: 'c1', text: 'newer', updatedAt: 2_000 }),
      draft({ chatId: 'c2', text: 'fresh', updatedAt: 2_000 }),
    ])

    expect(store().byChat.c1.text).toBe('newer')
    expect(store().byChat.c2.text).toBe('fresh')
  })
})

describe('clear', () => {
  it('drops every draft', () => {
    store().merge([draft({ chatId: 'c1' }), draft({ chatId: 'c2' })])
    store().clear()
    expect(store().byChat).toEqual({})
  })

  it('is what keeps one account`s unsent text off another account`s screen', () => {
    store().merge([draft()])
    store().clear()
    expect(store().byChat.c1).toBeUndefined()
  })
})
