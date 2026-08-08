'use client'

import { useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import {
  chatKindFromString,
  useChatStore,
  useReceiptStore,
  useTypingStore,
} from '@/entities/chat'
import { useDraftStore } from '@/entities/draft'
import { fromWire, updateHistory, upsertMessage, useReactionStore } from '@/entities/message'
import { applyWirePoll } from '@/entities/poll'
import { useSessionStore } from '@/entities/session'
import { useUserDirectory } from '@/entities/user'
import { queryKeys, useSynapseClient } from '@/shared/api'

/**
 * The single bridge from protocol events into application state.
 *
 * Every unsolicited frame the gateway pushes lands here and is routed to exactly
 * one owner: durable message data into the TanStack Query cache, derived chat
 * metadata into the chat registry, ephemeral signals (typing, presence,
 * receipts) into their own small stores. Keeping this in one hook — mounted once
 * — is what stops components from each growing their own socket subscription and
 * quietly disagreeing about state.
 */
export function useRealtimeSync(): void {
  const client = useSynapseClient()
  const queryClient = useQueryClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  useEffect(() => {
    if (!selfId) return

    const applyMessage = useChatStore.getState().applyMessage
    const upsertChat = useChatStore.getState().upsert

    const unsubscribers = [
      // A message delivered live by fanout. Note the gateway echoes a message
      // back to its own sender too, so this also fires for messages we sent —
      // `upsertMessage` collapses it onto the optimistic row.
      client.on('message', (message) => {
        applyMessage(message, selfId)
        updateHistory(queryClient, message.chatId, (data) =>
          upsertMessage(data, fromWire(message, selfId)),
        )
      }),

      client.on('read', (receipt) => {
        useReceiptStore.getState().apply(receipt.chatId, receipt.userId, receipt.upToChatSeq)
      }),

      client.on('typing', (typing) => {
        if (typing.userId === selfId) return
        useTypingStore.getState().mark(typing.chatId, typing.userId, typing.active)
      }),

      client.on('presence', (presence) => {
        useUserDirectory
          .getState()
          .setPresence(presence.userId, presence.online, presence.lastSeenMs)
      }),

      // Sent when a chat is created through us; the only moment the server tells
      // this client a chat's type, title and owner.
      client.on('chatInfo', (info) => {
        upsertChat({
          id: info.chatId,
          kind: chatKindFromString(info.type),
          title: info.title || info.chatId,
          ownerId: info.ownerId,
          provisional: false,
          updatedAt: Date.now(),
        })
      }),

      // Someone reacted. The body carries the full post-change tally, so the
      // store is replaced rather than incremented.
      client.on('reaction', (update) => {
        useReactionStore
          .getState()
          .apply(
            update.messageId,
            update.counts ?? {},
            update.emoji,
            update.added,
            update.userId === selfId,
          )
      }),

      // Pins are chat-wide: refetch rather than patch, since the push carries
      // the whole new set anyway and the query owns that data.
      client.on('pinned', (pinned) => {
        queryClient.invalidateQueries({ queryKey: queryKeys.pins(pinned.chatId) })
      }),

      // A draft written on another device of this same user.
      client.on('drafts', (drafts) => {
        useDraftStore.getState().merge(
          drafts.drafts.map((draft) => ({
            chatId: draft.chatId,
            text: draft.text,
            replyTo: draft.replyTo,
            updatedAt: draft.updatedAt,
          })),
        )
      }),

      client.on('poll', (poll) => applyWirePoll(poll)),

      client.on('error', (error) => {
        // Uncorrelated errors are informational here: anything tied to a request
        // already rejected that request's promise.
        console.warn('[synapse] gateway error', error.code, error.message)
      }),
    ]

    return () => unsubscribers.forEach((unsubscribe) => unsubscribe())
  }, [client, queryClient, selfId])

  // Typing indicators expire on their own — the "stopped typing" frame is not
  // guaranteed to arrive (it is droppable under load).
  useEffect(() => {
    const interval = setInterval(() => useTypingStore.getState().sweep(), 1500)
    return () => clearInterval(interval)
  }, [])
}
