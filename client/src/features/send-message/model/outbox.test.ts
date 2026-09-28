import { beforeEach, describe, expect, it, vi } from 'vitest'

import { readStorage } from '@/shared/lib/storage'
import { selectPendingCount, useOutboxStore, type OutboxItem } from './outbox'

const item = (overrides: Partial<OutboxItem> = {}): OutboxItem => ({
  dedupKey: 'k1',
  target: 'c1',
  text: 'hello',
  createdAt: 1_000,
  status: 'pending',
  ...overrides,
})

const store = () => useOutboxStore.getState()
const persisted = (ownerId: string) =>
  readStorage<OutboxItem[]>(`SyncApp:outbox:${ownerId}`, [])

beforeEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
  store().reset()
  store().load('u1')
})

describe('enqueue', () => {
  it('adds an item', () => {
    store().enqueue(item())
    expect(store().items).toHaveLength(1)
  })

  it('keeps queue order', () => {
    // Messages must leave in the order the user typed them.
    store().enqueue(item({ dedupKey: 'k1' }))
    store().enqueue(item({ dedupKey: 'k2' }))
    store().enqueue(item({ dedupKey: 'k3' }))

    expect(store().items.map((entry) => entry.dedupKey)).toEqual(['k1', 'k2', 'k3'])
  })

  /**
   * Persistence is the entire point: the outbox exists to survive exactly the
   * events that lose in-memory state — a reload on a dead connection, a crashed
   * tab, a laptop closed mid-send.
   */
  it('persists immediately', () => {
    store().enqueue(item())
    expect(persisted('u1')).toHaveLength(1)
  })

  it('persists under a per-account key', () => {
    // Two accounts in one browser must not inherit each other's unsent messages.
    store().enqueue(item())
    store().load('u2')
    expect(store().items).toEqual([])
    expect(persisted('u1')).toHaveLength(1)
  })

  it('keeps the dedup key, which is what makes a replay safe', () => {
    // The server resolves a repeat of (sender, dedup_key) to the stored message.
    // Losing the key would turn every reconnect replay into a duplicate send.
    store().enqueue(item({ dedupKey: 'stable-key' }))
    expect(persisted('u1')[0].dedupKey).toBe('stable-key')
  })

  it('queues only the media reference, not the bytes', () => {
    // The upload happened before the message was enqueued, so a replay re-sends
    // a small descriptor rather than the whole file.
    const attachment = {
      kind: 'image',
      mediaRef: 'media-1',
      filename: 'cat.png',
      mime: 'image/png',
      size: 1024,
      durationMs: 0,
      waveform: [],
      width: 100,
      height: 100,
      thumbRef: '',
    }
    store().enqueue(item({ attachment }))

    expect(persisted('u1')[0].attachment).toEqual(attachment)
  })

  it('keeps the reply target and self-destruct window', () => {
    store().enqueue(item({ replyTo: 'm7', ttlSeconds: 60 }))
    expect(persisted('u1')[0]).toMatchObject({ replyTo: 'm7', ttlSeconds: 60 })
  })

  it('accepts an "@username" target for a chat that does not exist yet', () => {
    store().enqueue(item({ target: '@bob' }))
    expect(store().items[0].target).toBe('@bob')
  })

  it('keeps the item in memory when storage fails', () => {
    // A full quota should cost durability, not the message itself.
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })

    expect(() => store().enqueue(item())).not.toThrow()
    expect(store().items).toHaveLength(1)
  })
})

describe('update', () => {
  it('patches an item by dedup key', () => {
    store().enqueue(item())
    store().update('k1', { status: 'failed', error: 'rate limited' })

    expect(store().items[0]).toMatchObject({ status: 'failed', error: 'rate limited' })
  })

  it('leaves other fields intact', () => {
    store().enqueue(item({ text: 'hello', replyTo: 'm7' }))
    store().update('k1', { status: 'failed' })

    expect(store().items[0]).toMatchObject({ text: 'hello', replyTo: 'm7' })
  })

  it('persists the patch', () => {
    // A failure marked only in memory would come back as "pending" after a
    // reload and be retried silently.
    store().enqueue(item())
    store().update('k1', { status: 'failed', error: 'forbidden' })

    expect(persisted('u1')[0]).toMatchObject({ status: 'failed', error: 'forbidden' })
  })

  it('touches only the addressed item', () => {
    store().enqueue(item({ dedupKey: 'k1' }))
    store().enqueue(item({ dedupKey: 'k2' }))
    store().update('k1', { status: 'failed' })

    expect(store().items[1].status).toBe('pending')
  })

  it('does nothing for an unknown key', () => {
    // A SEND_ACK can arrive for an item another tab already removed.
    store().enqueue(item())
    expect(() => store().update('ghost', { status: 'failed' })).not.toThrow()
    expect(store().items).toHaveLength(1)
  })

  it('can move a failed item back to pending for a retry', () => {
    store().enqueue(item())
    store().update('k1', { status: 'failed', error: 'offline' })
    store().update('k1', { status: 'pending', error: undefined })

    expect(store().items[0].status).toBe('pending')
  })
})

describe('remove', () => {
  it('drops an acknowledged item', () => {
    store().enqueue(item())
    store().remove('k1')
    expect(store().items).toEqual([])
  })

  it('persists the removal', () => {
    // An item left in storage after its SEND_ACK would be replayed on the next
    // reconnect — harmless thanks to the dedup key, but it would also resurrect
    // the pending bubble in the UI.
    store().enqueue(item())
    store().remove('k1')
    expect(persisted('u1')).toEqual([])
  })

  it('leaves the rest of the queue', () => {
    store().enqueue(item({ dedupKey: 'k1' }))
    store().enqueue(item({ dedupKey: 'k2' }))
    store().remove('k1')

    expect(store().items.map((entry) => entry.dedupKey)).toEqual(['k2'])
  })

  it('preserves order after a removal from the middle', () => {
    for (const key of ['k1', 'k2', 'k3']) store().enqueue(item({ dedupKey: key }))
    store().remove('k2')

    expect(store().items.map((entry) => entry.dedupKey)).toEqual(['k1', 'k3'])
  })

  it('does nothing for an unknown key', () => {
    store().enqueue(item())
    expect(() => store().remove('ghost')).not.toThrow()
    expect(store().items).toHaveLength(1)
  })
})

describe('load', () => {
  it('restores a queue written before a reload', () => {
    store().enqueue(item({ dedupKey: 'k1' }))
    store().enqueue(item({ dedupKey: 'k2' }))

    store().reset()
    store().load('u1')

    expect(store().items.map((entry) => entry.dedupKey)).toEqual(['k1', 'k2'])
  })

  it('starts empty for an account with no queue', () => {
    store().load('brand-new')
    expect(store().items).toEqual([])
  })

  it('switches accounts without leaking items', () => {
    store().enqueue(item({ dedupKey: 'u1-item' }))
    store().load('u2')
    expect(store().items).toEqual([])

    store().enqueue(item({ dedupKey: 'u2-item' }))
    store().load('u1')
    expect(store().items.map((entry) => entry.dedupKey)).toEqual(['u1-item'])
  })

  it('falls back to an empty queue on corrupt storage', () => {
    localStorage.setItem('SyncApp:outbox:u3', '{not json')
    expect(() => store().load('u3')).not.toThrow()
    expect(store().items).toEqual([])
  })

  it('preserves a failed status across a reload', () => {
    store().enqueue(item())
    store().update('k1', { status: 'failed', error: 'forbidden' })
    store().reset()
    store().load('u1')

    expect(store().items[0]).toMatchObject({ status: 'failed', error: 'forbidden' })
  })
})

describe('reset', () => {
  it('empties the in-memory queue', () => {
    store().enqueue(item())
    store().reset()
    expect(store().items).toEqual([])
    expect(store().ownerId).toBeNull()
  })

  it('leaves the persisted queue alone so logging back in recovers it', () => {
    // Logging out should not throw away messages the user wrote and never sent.
    store().enqueue(item())
    store().reset()
    expect(persisted('u1')).toHaveLength(1)
  })

  it('stops writes from landing under the previous account`s key', () => {
    store().enqueue(item({ dedupKey: 'k1' }))
    store().reset()
    store().enqueue(item({ dedupKey: 'orphan' }))

    expect(persisted('u1').map((entry) => entry.dedupKey)).toEqual(['k1'])
  })
})

describe('selectPendingCount', () => {
  it('counts the queue', () => {
    store().enqueue(item({ dedupKey: 'k1' }))
    store().enqueue(item({ dedupKey: 'k2' }))
    expect(selectPendingCount(store())).toBe(2)
  })

  it('is 0 for an empty queue', () => {
    expect(selectPendingCount(store())).toBe(0)
  })

  it('counts failed items too — they are still unsent', () => {
    store().enqueue(item())
    store().update('k1', { status: 'failed' })
    expect(selectPendingCount(store())).toBe(1)
  })
})
