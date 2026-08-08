'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { MsgType, queryKeys, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

export interface PinnedMessage {
  messageId: string
  pinnedBy: string
  pinnedAt: number
}

/**
 * A chat's pinned messages.
 *
 * Pins are chat-wide (unlike drafts, which are private to one user), so every
 * member sees the same set and the server pushes PINNED to all of them when it
 * changes — the realtime bridge invalidates this query on that push.
 */
export function useChatPins(chatId: string) {
  const client = useSynapseClient()
  const connected = useIsConnected()

  return useQuery({
    queryKey: queryKeys.pins(chatId),
    enabled: connected && chatId.length > 0 && !chatId.startsWith('@'),
    staleTime: 60_000,
    queryFn: async (): Promise<PinnedMessage[]> => {
      const reply = await client.request<Wire.Pinned>(
        MsgType.PIN_LIST,
        { chatId, messageId: '' },
        { expect: MsgType.PINNED },
      )
      return reply.body.pins.map((pin) => ({
        messageId: pin.messageId,
        pinnedBy: pin.pinnedBy,
        pinnedAt: pin.pinnedAt,
      }))
    },
  })
}

export function useTogglePin(chatId: string) {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ messageId, pinned }: { messageId: string; pinned: boolean }) => {
      const reply = await client.request<Wire.Pinned>(
        pinned ? MsgType.UNPIN : MsgType.PIN,
        { chatId, messageId },
        { expect: MsgType.PINNED },
      )
      return reply.body
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.pins(chatId) }),
  })
}
