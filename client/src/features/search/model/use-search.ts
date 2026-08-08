'use client'

import { useQuery } from '@tanstack/react-query'

import { MsgType, queryKeys, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

export interface SearchHit {
  messageId: string
  chatId: string
  senderId: string
  seq: number
  text: string
}

/**
 * Full-text search across the user's chats.
 *
 * Server-side and permission-filtered: the indexer only returns hits from chats
 * the caller is a member of, which is why this cannot be replaced by filtering
 * the local cache. SEARCH is rate-limited per connection, so the caller is
 * expected to debounce — `enabled` keeps it off until the query is meaningful.
 */
export function useMessageSearch(query: string, limit = 30) {
  const client = useSynapseClient()
  const connected = useIsConnected()
  const trimmed = query.trim()

  return useQuery({
    queryKey: queryKeys.search(trimmed),
    enabled: connected && trimmed.length >= 2,
    staleTime: 30_000,
    queryFn: async (): Promise<SearchHit[]> => {
      const reply = await client.request<Wire.SearchResults>(
        MsgType.SEARCH,
        { query: trimmed, limit },
        { expect: MsgType.SEARCH_RESULTS },
      )
      return reply.body.hits.map((hit) => ({
        messageId: hit.messageId,
        chatId: hit.chatId,
        senderId: hit.senderId,
        seq: hit.seq,
        text: hit.text,
      }))
    },
  })
}
