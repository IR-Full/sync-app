import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { queryKeys } from '@/shared/api'
import {
  flattenHistory,
  removeMessage,
  updateHistory,
  upsertMessage,
  type HistoryData,
} from './history-cache'
import type { ChatMessage } from './types'

function message(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'm1',
    chatId: 'c1',
    senderId: 'u1',
    text: 'hello',
    seq: 1,
    timestamp: 1_000,
    edited: false,
    deleted: false,
    replyTo: '',
    mediaRef: '',
    attachment: null,
    forward: null,
    threadRoot: '',
    replyCount: 0,
    expiresAt: 0,
    outgoing: true,
    status: 'sent',
    ...overrides,
  }
}

const page = (messages: ChatMessage[], nextBefore = 0, done = false) => ({
  messages,
  nextBefore,
  done,
})

const data = (pages: ReturnType<typeof page>[]): HistoryData => ({
  pages,
  pageParams: pages.map((_, index) => index),
})

describe('upsertMessage', () => {
  it('seeds a cache that does not exist yet', () => {
    // A live message can arrive before the first HISTORY page has landed.
    const result = upsertMessage(undefined, message())
    expect(result.pages).toHaveLength(1)
    expect(result.pages[0].messages).toEqual([message()])
  })

  it('seeds a cache whose page list is empty', () => {
    const result = upsertMessage({ pages: [], pageParams: [] }, message())
    expect(result.pages[0].messages).toEqual([message()])
  })

  it('prepends a new message to the newest page', () => {
    // Pages are newest-first and each page is newest-first within itself, so the
    // head of page 0 is where a live message belongs.
    const before = data([page([message({ id: 'm1', seq: 1 })])])
    const after = upsertMessage(before, message({ id: 'm2', seq: 2 }))

    expect(after.pages[0].messages.map((item) => item.id)).toEqual(['m2', 'm1'])
  })

  it('does not touch older pages when prepending', () => {
    const older = page([message({ id: 'm0', seq: 0 })])
    const before = data([page([message({ id: 'm1', seq: 1 })]), older])
    const after = upsertMessage(before, message({ id: 'm2', seq: 2 }))

    expect(after.pages[1]).toBe(older)
  })

  it('replaces a message already present, in place', () => {
    const before = data([page([message({ id: 'm1', text: 'old' })])])
    const after = upsertMessage(before, message({ id: 'm1', text: 'edited', edited: true }))

    expect(after.pages[0].messages).toHaveLength(1)
    expect(after.pages[0].messages[0]).toMatchObject({ text: 'edited', edited: true })
  })

  it('finds and replaces a message on an older page', () => {
    const before = data([
      page([message({ id: 'm2', seq: 2 })]),
      page([message({ id: 'm1', seq: 1, text: 'old' })]),
    ])
    const after = upsertMessage(before, message({ id: 'm1', seq: 1, text: 'edited' }))

    expect(after.pages[1].messages[0].text).toBe('edited')
    expect(after.pages[0].messages).toHaveLength(1)
  })

  /**
   * Fanout echoes a message back to its own sender, so a message we just sent
   * optimistically arrives again as a live NEW. Collapsing the two is what keeps
   * it from appearing twice — and the optimistic row is keyed by its dedup key
   * until SEND_ACK assigns a real id, so the match has to work in both
   * directions.
   */
  it('collapses a server echo onto the optimistic row by dedup key', () => {
    const optimistic = message({
      id: 'dedup-1',
      dedupKey: 'dedup-1',
      seq: 0,
      status: 'pending',
    })
    const before = data([page([optimistic])])

    const after = upsertMessage(
      before,
      message({ id: 'server-1', dedupKey: 'dedup-1', seq: 7, status: 'sent' }),
    )

    expect(after.pages[0].messages).toHaveLength(1)
    expect(after.pages[0].messages[0]).toMatchObject({ id: 'server-1', seq: 7, status: 'sent' })
  })

  it('collapses when the echo carries no dedup key but matches the optimistic id', () => {
    const optimistic = message({
      id: 'dedup-1',
      dedupKey: 'dedup-1',
      seq: 0,
      status: 'pending',
    })
    const before = data([page([optimistic])])

    const after = upsertMessage(before, message({ id: 'dedup-1', seq: 7, status: 'sent' }))

    expect(after.pages[0].messages).toHaveLength(1)
    expect(after.pages[0].messages[0].seq).toBe(7)
  })

  it('does not merge two different messages that both lack a dedup key', () => {
    const before = data([page([message({ id: 'm1' })])])
    const after = upsertMessage(before, message({ id: 'm2' }))
    expect(after.pages[0].messages).toHaveLength(2)
  })

  it('does not merge messages whose dedup keys differ', () => {
    const before = data([page([message({ id: 'a', dedupKey: 'k1' })])])
    const after = upsertMessage(before, message({ id: 'b', dedupKey: 'k2' }))
    expect(after.pages[0].messages).toHaveLength(2)
  })

  it('merges rather than overwrites, keeping fields the update omits', () => {
    const before = data([page([message({ id: 'm1', dedupKey: 'k1', text: 'hello' })])])
    const after = upsertMessage(before, message({ id: 'm1', text: 'hello', seq: 9 }))

    expect(after.pages[0].messages[0].dedupKey).toBe('k1')
  })

  it('returns a new top-level object so React Query sees a change', () => {
    const before = data([page([message({ id: 'm1' })])])
    expect(upsertMessage(before, message({ id: 'm2' }))).not.toBe(before)
  })

  it('preserves the page cursors', () => {
    // `nextBefore` is what the next page request is keyed on; losing it would
    // restart pagination from the newest message.
    const before = data([page([message({ id: 'm1' })], 42, false)])
    const after = upsertMessage(before, message({ id: 'm2' }))

    expect(after.pages[0].nextBefore).toBe(42)
    expect(after.pageParams).toEqual(before.pageParams)
  })
})

describe('removeMessage', () => {
  it('removes a message', () => {
    const before = data([page([message({ id: 'm1' }), message({ id: 'm2' })])])
    const after = removeMessage(before, 'm1')
    expect(after!.pages[0].messages.map((item) => item.id)).toEqual(['m2'])
  })

  it('removes from an older page too', () => {
    const before = data([page([message({ id: 'm2' })]), page([message({ id: 'm1' })])])
    const after = removeMessage(before, 'm1')
    expect(after!.pages[1].messages).toEqual([])
  })

  it('leaves the cache alone for an unknown id', () => {
    const before = data([page([message({ id: 'm1' })])])
    expect(removeMessage(before, 'nope')!.pages[0].messages).toHaveLength(1)
  })

  it('passes undefined through', () => {
    // A delete can arrive for a chat whose history was never opened.
    expect(removeMessage(undefined, 'm1')).toBeUndefined()
  })

  it('keeps the page structure so pagination still works', () => {
    const before = data([page([message({ id: 'm1' })], 42, true)])
    const after = removeMessage(before, 'm1')
    expect(after!.pages[0]).toMatchObject({ nextBefore: 42, done: true })
  })
})

describe('flattenHistory', () => {
  it('returns an empty transcript for no data', () => {
    expect(flattenHistory(undefined)).toEqual([])
  })

  it('reverses the newest-first pages into an oldest-first transcript', () => {
    // The wire order is newest-first (so the first page is the one to render);
    // the transcript reads top-to-bottom oldest-first.
    const before = data([
      page([message({ id: 'm3', seq: 3 }), message({ id: 'm2', seq: 2 })]),
      page([message({ id: 'm1', seq: 1 })]),
    ])
    expect(flattenHistory(before).map((item) => item.id)).toEqual(['m1', 'm2', 'm3'])
  })

  it('orders by sequence, not by arrival', () => {
    const before = data([page([message({ id: 'b', seq: 9 }), message({ id: 'a', seq: 2 })])])
    expect(flattenHistory(before).map((item) => item.id)).toEqual(['a', 'b'])
  })

  /**
   * A pending message has no sequence yet (the server assigns it), so a naive
   * numeric sort would file it at the very top of the transcript — above
   * messages sent months ago — instead of at the bottom where the user just
   * typed it.
   */
  it('puts pending messages at the end', () => {
    const before = data([
      page([
        message({ id: 'pending', seq: 0, timestamp: 5_000, status: 'pending' }),
        message({ id: 'sent', seq: 9, timestamp: 1_000 }),
      ]),
    ])
    expect(flattenHistory(before).map((item) => item.id)).toEqual(['sent', 'pending'])
  })

  it('orders several pending messages by the time they were composed', () => {
    const before = data([
      page([
        message({ id: 'second', seq: 0, timestamp: 2_000, status: 'pending' }),
        message({ id: 'first', seq: 0, timestamp: 1_000, status: 'pending' }),
      ]),
    ])
    expect(flattenHistory(before).map((item) => item.id)).toEqual(['first', 'second'])
  })

  it('handles a transcript made only of pending messages', () => {
    // Composing offline: nothing has a sequence yet.
    const before = data([
      page([
        message({ id: 'b', seq: 0, timestamp: 2_000, status: 'pending' }),
        message({ id: 'a', seq: 0, timestamp: 1_000, status: 'pending' }),
      ]),
    ])
    expect(flattenHistory(before).map((item) => item.id)).toEqual(['a', 'b'])
  })

  it('returns an empty transcript for pages with no messages', () => {
    expect(flattenHistory(data([page([])]))).toEqual([])
  })
})

describe('updateHistory', () => {
  it('writes through to the query cache under the history key', () => {
    const queryClient = new QueryClient()
    updateHistory(queryClient, 'c1', () => data([page([message({ id: 'm1' })])]))

    const stored = queryClient.getQueryData<HistoryData>(queryKeys.history('c1'))
    expect(stored!.pages[0].messages[0].id).toBe('m1')
  })

  it('passes the current cache into the updater', () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(queryKeys.history('c1'), data([page([message({ id: 'm1' })])]))

    updateHistory(queryClient, 'c1', (current) => upsertMessage(current, message({ id: 'm2' })))

    const stored = queryClient.getQueryData<HistoryData>(queryKeys.history('c1'))
    expect(stored!.pages[0].messages.map((item) => item.id)).toEqual(['m2', 'm1'])
  })

  it('passes undefined for a chat with no cached history', () => {
    const queryClient = new QueryClient()
    let seen: HistoryData | undefined | 'not-called' = 'not-called'
    updateHistory(queryClient, 'c1', (current) => {
      seen = current
      return current
    })
    expect(seen).toBeUndefined()
  })

  it('keeps chats in separate cache entries', () => {
    const queryClient = new QueryClient()
    updateHistory(queryClient, 'c1', () => data([page([message({ id: 'm1', chatId: 'c1' })])]))
    updateHistory(queryClient, 'c2', () => data([page([message({ id: 'm2', chatId: 'c2' })])]))

    expect(
      queryClient.getQueryData<HistoryData>(queryKeys.history('c1'))!.pages[0].messages[0].id,
    ).toBe('m1')
    expect(
      queryClient.getQueryData<HistoryData>(queryKeys.history('c2'))!.pages[0].messages[0].id,
    ).toBe('m2')
  })
})
