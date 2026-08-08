'use client'

import { useInfiniteQuery } from '@tanstack/react-query'

import { fromWire, type ChatMessage } from '@/entities/message'
import { useSessionStore } from '@/entities/session'
import { MsgType, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

interface ThreadPage {
  messages: ChatMessage[]
  nextAfter: number
  done: boolean
}

/**
 * The replies under a thread root.
 *
 * Pages *forward* (oldest first, `afterSeq` cursor) — the opposite direction to
 * chat history, because a thread is read from its beginning. Like HISTORY, the
 * page streams as NEW frames sharing the request id, terminated by THREAD_OK.
 *
 * The thread root is resolved server-side at write time, so this is one indexed
 * read rather than a walk up the reply chain.
 */
export function useThread(chatId: string, rootId: string, pageSize = 50) {
  const client = useSynapseClient()
  const connected = useIsConnected()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  return useInfiniteQuery<
    ThreadPage,
    Error,
    { pages: ThreadPage[] },
    readonly unknown[],
    number
  >({
    queryKey: ['thread', chatId, rootId],
    initialPageParam: 0,
    getNextPageParam: (lastPage) => (lastPage.done ? undefined : lastPage.nextAfter),
    enabled: connected && !!chatId && !!rootId && !!selfId,
    staleTime: Infinity,
    queryFn: async ({ pageParam }) => {
      const page = await client.requestStream<Wire.NewMessage, Wire.ThreadOK>(
        MsgType.THREAD,
        { chatId, rootId, afterSeq: pageParam, limit: pageSize },
        { itemType: MsgType.NEW, endType: MsgType.THREAD_OK },
      )
      const messages = page.items.map((item) => fromWire(item, selfId))
      return {
        messages,
        nextAfter: page.end.nextAfter,
        done: page.end.done || messages.length === 0,
      }
    },
  })
}
