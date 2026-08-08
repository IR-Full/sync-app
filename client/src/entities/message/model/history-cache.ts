import type { InfiniteData, QueryClient } from '@tanstack/react-query'

import type { ChatMessage } from './types'
import { queryKeys } from '@/shared/api'

/**
 * One HISTORY response.
 *
 * The gateway answers a HISTORY request by streaming the page as ordinary NEW
 * frames (so a client's normal ingest path handles them) and closing with
 * HISTORY_OK carrying the cursor. Messages come back newest-first, and
 * `nextBefore` is the oldest sequence in the page — feed it back as `beforeSeq`
 * to walk further into the past.
 */
export interface HistoryPage {
  messages: ChatMessage[]
  nextBefore: number
  done: boolean
}

export type HistoryData = InfiniteData<HistoryPage, number>

/**
 * Inserts (or replaces) a message in the newest page.
 *
 * Replacement matters more than insertion: fanout echoes a message back to its
 * own sender, so a message we just sent optimistically arrives again as a live
 * NEW. Matching on the server message id — and on the dedup key, which is the
 * optimistic row's id until SEND_ACK lands — collapses the two into one row
 * instead of showing the message twice.
 */
export function upsertMessage(
  data: HistoryData | undefined,
  message: ChatMessage,
): HistoryData {
  if (!data || data.pages.length === 0) {
    return {
      pages: [{ messages: [message], nextBefore: 0, done: false }],
      pageParams: [0],
    }
  }

  const identifies = (candidate: ChatMessage) =>
    candidate.id === message.id ||
    (!!message.dedupKey && candidate.dedupKey === message.dedupKey) ||
    (!!candidate.dedupKey && candidate.dedupKey === message.id)

  let replaced = false
  const pages = data.pages.map((page) => {
    if (!page.messages.some(identifies)) return page
    replaced = true
    return {
      ...page,
      messages: page.messages.map((candidate) =>
        identifies(candidate) ? { ...candidate, ...message } : candidate,
      ),
    }
  })

  if (replaced) return { ...data, pages }

  // New message: it belongs at the head of the newest page (index 0).
  const [newest, ...rest] = pages
  return {
    ...data,
    pages: [{ ...newest, messages: [message, ...newest.messages] }, ...rest],
  }
}

export function removeMessage(
  data: HistoryData | undefined,
  messageId: string,
): HistoryData | undefined {
  if (!data) return data
  return {
    ...data,
    pages: data.pages.map((page) => ({
      ...page,
      messages: page.messages.filter((message) => message.id !== messageId),
    })),
  }
}

/** Applies a cache update for one chat's history. */
export function updateHistory(
  queryClient: QueryClient,
  chatId: string,
  update: (data: HistoryData | undefined) => HistoryData | undefined,
): void {
  queryClient.setQueryData<HistoryData>(queryKeys.history(chatId), update)
}

/** Flattens the pages into a single oldest-first transcript. */
export function flattenHistory(data: HistoryData | undefined): ChatMessage[] {
  if (!data) return []
  const all = data.pages.flatMap((page) => page.messages)
  return all.sort((a, b) => {
    // Pending messages have no sequence yet; they always belong at the end.
    if (a.seq === 0 && b.seq === 0) return a.timestamp - b.timestamp
    if (a.seq === 0) return 1
    if (b.seq === 0) return -1
    return a.seq - b.seq
  })
}
