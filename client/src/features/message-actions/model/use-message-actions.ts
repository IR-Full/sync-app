'use client'

import { useQueryClient } from '@tanstack/react-query'
import { useCallback } from 'react'

import { useChatStore } from '@/entities/chat'
import {
  updateHistory,
  upsertMessage,
  useReactionStore,
  type ChatMessage,
} from '@/entities/message'
import { useSessionStore } from '@/entities/session'
import { MsgType, useSynapseClient, type Wire } from '@/shared/api'
import { createDedupKey } from '@/shared/lib/id'

/**
 * Editing and deleting.
 *
 * Both are fire-and-forget: the gateway answers only on failure. The change
 * comes back through the ordinary fanout path as a NEW frame carrying the
 * `edited` / `deleted` flags (the server publishes message.edited and
 * message.deleted onto the same subject fanout already delivers), so there is
 * nothing to merge here — the realtime bridge overwrites the row by id.
 *
 * The optimistic update exists only to make the change instant for the author;
 * the authoritative version lands a moment later.
 */
export function useMessageActions(chatId: string) {
  const client = useSynapseClient()
  const queryClient = useQueryClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  const edit = useCallback(
    (message: ChatMessage, text: string) => {
      const trimmed = text.trim()
      if (!trimmed || trimmed === message.text || !message.seq) return
      client.send(MsgType.EDIT, { chatId, messageId: message.id, text: trimmed })
      updateHistory(queryClient, chatId, (data) =>
        upsertMessage(data, { ...message, text: trimmed, edited: true }),
      )
    },
    [client, chatId, queryClient],
  )

  const remove = useCallback(
    (message: ChatMessage, forAll = true) => {
      if (!message.seq) return
      client.send(MsgType.DELETE, { chatId, messageId: message.id, forAll })
      updateHistory(queryClient, chatId, (data) =>
        upsertMessage(data, { ...message, text: '', deleted: true }),
      )
    },
    [client, chatId, queryClient],
  )

  /**
   * Toggles a reaction. The reply carries the post-change tally, so the reacting
   * client renders immediately instead of waiting for its own fanout round trip.
   */
  const react = useCallback(
    async (message: ChatMessage, emoji: string) => {
      const reply = await client.request<Wire.ReactUpdate>(
        MsgType.REACT,
        { chatId, messageId: message.id, emoji },
        { expect: MsgType.REACT_UPD },
      )
      const { counts, added } = reply.body
      useReactionStore.getState().apply(message.id, counts ?? {}, emoji, added, true)
    },
    [client, chatId],
  )

  return { edit, remove, react, selfId }
}

/**
 * Copies a message into another chat, keeping its provenance.
 *
 * FORWARD answers with a SEND_ACK for the *new* message, exactly like a normal
 * send — so it needs its own dedup key for the same idempotency guarantee.
 */
export function useForwardMessage() {
  const client = useSynapseClient()
  const upsertChat = useChatStore((store) => store.upsert)

  return useCallback(
    async (message: ChatMessage, toChatId: string) => {
      const ack = await client.request<Wire.SendAck>(
        MsgType.FORWARD,
        {
          fromChatId: message.chatId,
          messageId: message.id,
          toChatId,
          dedupKey: createDedupKey(),
        },
        { expect: MsgType.SEND_ACK },
      )
      // The destination may be a handle that just became a real chat.
      if (ack.body.chatId && ack.body.chatId !== toChatId) {
        upsertChat({ id: ack.body.chatId, kind: 'direct', title: toChatId, handle: toChatId })
      }
      return ack.body
    },
    [client, upsertChat],
  )
}
