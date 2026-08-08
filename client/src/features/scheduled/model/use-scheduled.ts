'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { MsgType, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

export interface ScheduledItem {
  id: string
  chatId: string
  text: string
  sendAt: number
}

const scheduledKey = (chatId: string) => ['scheduled', chatId] as const

/**
 * Messages queued for later delivery.
 *
 * The server runs a dispatcher that turns a due item into a real message, so a
 * scheduled send is not a client-side timer — closing the tab does not cancel
 * it. All three operations answer with the same SCHEDULED body.
 */
export function useScheduledMessages(chatId: string) {
  const client = useSynapseClient()
  const connected = useIsConnected()

  return useQuery({
    queryKey: scheduledKey(chatId),
    enabled: connected && chatId.length > 0 && !chatId.startsWith('@'),
    staleTime: 30_000,
    queryFn: async (): Promise<ScheduledItem[]> => {
      const reply = await client.request<Wire.Scheduled>(
        MsgType.SCHEDULE_LIST,
        { chatId },
        { expect: MsgType.SCHEDULED },
      )
      return reply.body.items.map((item) => ({
        id: item.id,
        chatId: item.chatId,
        text: item.text,
        sendAt: item.sendAt,
      }))
    },
  })
}

export function useScheduleMessage(chatId: string) {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({
      text,
      sendAt,
      ttlSeconds = 0,
    }: {
      text: string
      sendAt: number
      ttlSeconds?: number
    }) => {
      const reply = await client.request<Wire.Scheduled>(
        MsgType.SCHEDULE,
        { chatId, text, sendAt, ttlSeconds },
        { expect: MsgType.SCHEDULED },
      )
      return reply.body
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledKey(chatId) }),
  })
}

export function useCancelScheduled(chatId: string) {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (id: string) => {
      await client.request<Wire.Scheduled>(
        MsgType.SCHEDULE_CANCEL,
        { id },
        { expect: MsgType.SCHEDULED },
      )
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledKey(chatId) }),
  })
}
