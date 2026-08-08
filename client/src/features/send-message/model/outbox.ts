'use client'

import { create } from 'zustand'

import type { MessageAttachment } from '@/entities/message'
import { readStorage, writeStorage } from '@/shared/lib/storage'

export interface OutboxItem {
  /** idempotency key — stable across every retry of this message */
  dedupKey: string
  /** chat id, or "@username" for a direct chat that does not exist yet */
  target: string
  text: string
  createdAt: number
  status: 'pending' | 'failed'
  error?: string
  /**
   * Already-uploaded media. Only the reference is queued: the bytes went to the
   * media endpoint before the message was ever enqueued, so a replay after a
   * reconnect re-sends a small descriptor rather than the file.
   */
  attachment?: MessageAttachment | null
  replyTo?: string
  /** self-destruct window in seconds (0 = keep forever) */
  ttlSeconds?: number
}

interface OutboxState {
  ownerId: string | null
  items: OutboxItem[]
  load: (ownerId: string) => void
  reset: () => void
  enqueue: (item: OutboxItem) => void
  update: (dedupKey: string, patch: Partial<OutboxItem>) => void
  remove: (dedupKey: string) => void
}

function storageKey(ownerId: string): string {
  return `synapse:outbox:${ownerId}`
}

function persist(ownerId: string | null, items: OutboxItem[]): void {
  if (!ownerId) return
  writeStorage(storageKey(ownerId), items)
}

/**
 * Messages written but not yet acknowledged.
 *
 * Persisted, because the point of an outbox is to survive exactly the events
 * that lose in-memory state: a reload during a dead connection, a crashed tab, a
 * laptop closed mid-send. Each item keeps its dedup key, so replaying the whole
 * queue after a reconnect is safe — the server resolves a repeat of
 * `(sender, dedup_key)` to the stored message instead of writing a second one.
 */
export const useOutboxStore = create<OutboxState>((set, get) => ({
  ownerId: null,
  items: [],

  load: (ownerId) => {
    const items = readStorage<OutboxItem[]>(storageKey(ownerId), [])
    set({ ownerId, items })
  },

  reset: () => set({ ownerId: null, items: [] }),

  enqueue: (item) => {
    const items = [...get().items, item]
    persist(get().ownerId, items)
    set({ items })
  },

  update: (dedupKey, patch) => {
    const items = get().items.map((item) =>
      item.dedupKey === dedupKey ? { ...item, ...patch } : item,
    )
    persist(get().ownerId, items)
    set({ items })
  },

  remove: (dedupKey) => {
    const items = get().items.filter((item) => item.dedupKey !== dedupKey)
    persist(get().ownerId, items)
    set({ items })
  },
}))

export function selectPendingCount(state: OutboxState): number {
  return state.items.length
}
