import { beforeEach, describe, expect, it } from 'vitest'

import type { Wire } from '@/shared/api'
import { applyWirePoll, pollFromWire, usePollStore, type Poll } from './model'

const wire = (overrides: Partial<Wire.PollState> = {}): Wire.PollState =>
  ({
    pollId: 'p1',
    chatId: 'c1',
    messageId: 'm1',
    question: 'Lunch?',
    options: [
      { index: 0, text: 'Pizza', votes: 2 },
      { index: 1, text: 'Sushi', votes: 1 },
    ],
    totalVotes: 3,
    multiChoice: false,
    anonymous: false,
    closed: false,
    myVotes: [0],
    ...overrides,
  }) as Wire.PollState

const store = () => usePollStore.getState()

beforeEach(() => store().clear())

describe('pollFromWire', () => {
  it('maps every field across', () => {
    expect(pollFromWire(wire())).toEqual<Poll>({
      pollId: 'p1',
      chatId: 'c1',
      messageId: 'm1',
      question: 'Lunch?',
      options: [
        { index: 0, text: 'Pizza', votes: 2 },
        { index: 1, text: 'Sushi', votes: 1 },
      ],
      totalVotes: 3,
      multiChoice: false,
      anonymous: false,
      closed: false,
      myVotes: [0],
    })
  })

  it('defaults absent votes to an empty array', () => {
    // A poll nobody has voted in decodes `my_votes` to nothing; the UI maps over
    // it to highlight the user's choices, and `undefined.includes` would throw.
    expect(pollFromWire(wire({ myVotes: undefined })).myVotes).toEqual([])
  })

  it('preserves the option order the server sent', () => {
    // Option index is what a vote is cast by; reordering would misattribute it.
    const poll = pollFromWire(
      wire({
        options: [
          { index: 0, text: 'A', votes: 0 },
          { index: 1, text: 'B', votes: 0 },
          { index: 2, text: 'C', votes: 0 },
        ],
      }) as Wire.PollState,
    )
    expect(poll.options.map((option) => option.text)).toEqual(['A', 'B', 'C'])
  })

  it('carries the multi-choice and anonymous flags', () => {
    const poll = pollFromWire(wire({ multiChoice: true, anonymous: true }))
    expect(poll).toMatchObject({ multiChoice: true, anonymous: true })
  })

  it('carries several of our own votes for a multi-choice poll', () => {
    expect(pollFromWire(wire({ multiChoice: true, myVotes: [0, 1] })).myVotes).toEqual([0, 1])
  })

  it('handles a poll with no options', () => {
    expect(pollFromWire(wire({ options: [] })).options).toEqual([])
  })
})

describe('apply', () => {
  it('stores a poll by id', () => {
    store().apply(pollFromWire(wire()))
    expect(store().byId.p1.question).toBe('Lunch?')
  })

  it('indexes the poll by its message so the bubble can find it', () => {
    store().apply(pollFromWire(wire()))
    expect(store().byMessage.m1).toBe('p1')
  })

  /**
   * Every create/vote/close answers with the full POLL_STATE and the same body
   * fans out to the chat, so the tally is replaced wholesale. Incrementing
   * locally would let two voters' screens drift apart with no way to reconcile.
   */
  it('replaces an earlier tally wholesale', () => {
    store().apply(pollFromWire(wire()))
    store().apply(
      pollFromWire(
        wire({
          options: [
            { index: 0, text: 'Pizza', votes: 5 },
            { index: 1, text: 'Sushi', votes: 1 },
          ],
          totalVotes: 6,
        }) as Wire.PollState,
      ),
    )

    expect(store().byId.p1.options[0].votes).toBe(5)
    expect(store().byId.p1.totalVotes).toBe(6)
  })

  it('records a poll being closed', () => {
    store().apply(pollFromWire(wire()))
    store().apply(pollFromWire(wire({ closed: true })))
    expect(store().byId.p1.closed).toBe(true)
  })

  it('leaves the message index alone when the update carries no message id', () => {
    // A POLL_STATE pushed for a vote need not repeat which message hosts the
    // poll; dropping the mapping would orphan the bubble's tally.
    store().apply(pollFromWire(wire()))
    store().apply(pollFromWire(wire({ messageId: '' })))

    expect(store().byMessage.m1).toBe('p1')
  })

  it('keeps polls independent', () => {
    store().apply(pollFromWire(wire({ pollId: 'p1', messageId: 'm1' })))
    store().apply(pollFromWire(wire({ pollId: 'p2', messageId: 'm2', question: 'Other?' })))

    expect(store().byId.p1.question).toBe('Lunch?')
    expect(store().byId.p2.question).toBe('Other?')
    expect(store().byMessage).toEqual({ m1: 'p1', m2: 'p2' })
  })
})

describe('applyWirePoll', () => {
  it('decodes and stores in one step', () => {
    applyWirePoll(wire())
    expect(store().byId.p1.question).toBe('Lunch?')
    expect(store().byMessage.m1).toBe('p1')
  })
})

describe('clear', () => {
  it('drops both indexes', () => {
    applyWirePoll(wire())
    store().clear()
    expect(store().byId).toEqual({})
    expect(store().byMessage).toEqual({})
  })
})
