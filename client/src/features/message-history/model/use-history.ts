'use client'

import { useInfiniteQuery } from '@tanstack/react-query'

import { fromWire, type ChatMessage } from '@/entities/message'
import { useSessionStore } from '@/entities/session'
import { MsgType, queryKeys, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'
import { config } from '@/shared/config/env'

import type { HistoryPage } from '@/entities/message'

/**
 * Paged chat history.
 *
 * This is the one part of the protocol with a genuine request/response shape and
 * a cursor, which is exactly what TanStack Query's infinite queries are for:
 * caching per chat, deduping concurrent requests, and tracking the paging state.
 * Live messages are *not* fetched here — they arrive over the socket and are
 * written into this same cache by the realtime bridge.
 */
export function useChatHistory(chatId: string, fetchEnabled = true) {
  const client = useSynapseClient()
  const connected = useIsConnected()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  return useInfiniteQuery<
    HistoryPage,
    Error,
    { pages: HistoryPage[] },
    readonly unknown[],
    number
  >({
    queryKey: queryKeys.history(chatId),
    // `beforeSeq: 0` means "from the newest"; later pages pass the cursor back.
    initialPageParam: 0,
    getNextPageParam: (lastPage) => (lastPage.done ? undefined : lastPage.nextBefore),
    // A disabled query still subscribes to its cache entry, which is exactly
    // what a not-yet-created direct chat needs: nothing to fetch, but optimistic
    // messages written under this key must still re-render the view.
    enabled: fetchEnabled && connected && chatId.length > 0 && selfId.length > 0,
    // History is immutable once written, and the socket pushes anything new, so
    // there is no reason to refetch a page the cache already holds.
    staleTime: Infinity,
    gcTime: 10 * 60_000,
    refetchOnWindowFocus: false,
    retry: 1,
    queryFn: async ({ pageParam }) => {
      const page = await client.requestStream<Wire.NewMessage, Wire.HistoryOK>(
        MsgType.HISTORY,
        { chatId, beforeSeq: pageParam, limit: config.historyPageSize },
        { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK },
      )
      const messages: ChatMessage[] = page.items.map((item) => fromWire(item, selfId))
      return {
        messages,
        nextBefore: page.end.nextBefore,
        // The gateway sets `done` when it returned fewer rows than asked for.
        done: page.end.done || messages.length === 0,
      }
    },
  })
}
