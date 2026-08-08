'use client'

import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef } from 'react'

import { useChatStore } from '@/entities/chat'
import {
  draftMessage,
  updateHistory,
  upsertMessage,
  type ChatMessage,
  type MessageAttachment,
} from '@/entities/message'
import { useSessionStore } from '@/entities/session'
import {
  MsgType,
  ProtocolError,
  useConnectionState,
  useSynapseClient,
  type Wire,
} from '@/shared/api'
import { createDedupKey } from '@/shared/lib/id'

import { useOutboxStore, type OutboxItem } from './outbox'

/** Targets starting with "@" are handles the gateway resolves into a direct chat. */
export function isHandleTarget(target: string): boolean {
  return target.startsWith('@')
}

export interface SendOptions {
  /** uploaded media to attach (the bytes are already stored server-side) */
  attachment?: MessageAttachment | null
  /** id of the message being replied to; also anchors the thread */
  replyTo?: string
  /** self-destruct window in seconds (0 = keep forever) */
  ttlSeconds?: number
}

function toOptimistic(item: OutboxItem, selfId: string, chatId: string): ChatMessage {
  return draftMessage({
    id: item.dedupKey,
    chatId,
    senderId: selfId,
    text: item.text,
    timestamp: item.createdAt,
    replyTo: item.replyTo ?? '',
    mediaRef: item.attachment?.mediaRef ?? '',
    attachment: item.attachment ?? null,
    outgoing: true,
    status: item.status === 'failed' ? 'failed' : 'pending',
    dedupKey: item.dedupKey,
    error: item.error,
  })
}

/** The SEND body for an outbox item — one place so retries send exactly what the first attempt did. */
function toWireSend(item: OutboxItem): Record<string, unknown> {
  return {
    chatId: item.target,
    dedupKey: item.dedupKey,
    text: item.text,
    replyTo: item.replyTo ?? '',
    mediaRef: item.attachment?.mediaRef ?? '',
    attachment: item.attachment ?? undefined,
    ttlSeconds: item.ttlSeconds ?? 0,
  }
}

/**
 * Sending, with an offline queue and idempotent retries.
 *
 * The flow deliberately mirrors what the server guarantees: the message is
 * rendered immediately (optimistic), queued with a dedup key, and only marked
 * delivered when SEND_ACK comes back with the durable id and chat sequence. A
 * send attempted while disconnected simply stays in the queue and is replayed on
 * reconnect — safe to do blindly, because the dedup key makes a duplicate
 * delivery impossible.
 */
export function useSendMessage(target: string, onChatResolved?: (chatId: string) => void) {
  const client = useSynapseClient()
  const state = useConnectionState()
  const queryClient = useQueryClient()
  const selfId = useSessionStore((session) => session.session?.userId ?? '')
  const applyChatMessage = useChatStore((store) => store.applyMessage)
  const upsertChat = useChatStore((store) => store.upsert)
  const outbox = useOutboxStore()

  // Kept in a ref so `deliver` does not have to be rebuilt whenever the caller
  // passes a new inline callback.
  const resolvedRef = useRef(onChatResolved)
  useEffect(() => {
    resolvedRef.current = onChatResolved
  }, [onChatResolved])

  const deliver = useCallback(
    async (item: OutboxItem) => {
      try {
        const ack = await client.request<Wire.SendAck>(MsgType.SEND, toWireSend(item), {
          expect: MsgType.SEND_ACK,
        })
        const { chatId, messageId, chatSeq, timestamp } = ack.body

        const confirmed = draftMessage({
          id: messageId,
          chatId,
          senderId: selfId,
          text: item.text,
          seq: chatSeq,
          timestamp,
          replyTo: item.replyTo ?? '',
          mediaRef: item.attachment?.mediaRef ?? '',
          attachment: item.attachment ?? null,
          outgoing: true,
          status: 'sent',
          dedupKey: item.dedupKey,
        })

        // A handle target ("@bob") resolves to a real chat id only now, so the
        // optimistic row lives under the handle and has to be moved across.
        if (chatId !== item.target) {
          updateHistory(queryClient, item.target, () => undefined)
          upsertChat({
            id: chatId,
            kind: 'direct',
            title: item.target,
            handle: item.target,
            provisional: false,
          })
          resolvedRef.current?.(chatId)
        }

        updateHistory(queryClient, chatId, (data) => upsertMessage(data, confirmed))
        applyChatMessage(
          {
            messageId,
            chatId,
            senderId: selfId,
            chatSeq,
            text: item.text,
            timestamp,
            edited: false,
            deleted: false,
          } as Wire.NewMessage,
          selfId,
        )
        outbox.remove(item.dedupKey)
      } catch (error) {
        // Transient failures stay queued for the next reconnect; a rejection the
        // server will keep making (forbidden, blocked, bad argument) is marked
        // failed so the user can see it and decide.
        const permanent = error instanceof ProtocolError && !error.retryable
        const message = error instanceof Error ? error.message : 'send failed'
        if (permanent) {
          outbox.update(item.dedupKey, { status: 'failed', error: message })
          updateHistory(queryClient, item.target, (data) =>
            upsertMessage(data, {
              ...toOptimistic(item, selfId, item.target),
              status: 'failed',
              error: message,
            }),
          )
        }
      }
    },
    [client, queryClient, selfId, applyChatMessage, upsertChat, outbox],
  )

  const send = useCallback(
    (text: string, options: SendOptions = {}) => {
      const trimmed = text.trim()
      // An attachment with no caption is a valid message, and so is bare text.
      if ((!trimmed && !options.attachment) || !selfId) return

      const item: OutboxItem = {
        dedupKey: createDedupKey(),
        target,
        text: trimmed,
        createdAt: Date.now(),
        status: 'pending',
        attachment: options.attachment ?? null,
        replyTo: options.replyTo ?? '',
        ttlSeconds: options.ttlSeconds ?? 0,
      }
      outbox.enqueue(item)
      updateHistory(queryClient, target, (data) =>
        upsertMessage(data, toOptimistic(item, selfId, target)),
      )
      if (state === 'ready') void deliver(item)
    },
    [target, selfId, outbox, queryClient, state, deliver],
  )

  const retry = useCallback(
    (message: ChatMessage) => {
      const item = outbox.items.find((candidate) => candidate.dedupKey === message.dedupKey)
      if (!item) return
      outbox.update(item.dedupKey, { status: 'pending', error: undefined })
      updateHistory(queryClient, item.target, (data) =>
        upsertMessage(data, { ...message, status: 'pending', error: undefined }),
      )
      if (state === 'ready') void deliver({ ...item, status: 'pending' })
    },
    [outbox, queryClient, state, deliver],
  )

  return { send, retry }
}

/**
 * Drains the outbox whenever the connection comes back.
 *
 * Mounted once at the app level rather than per chat window, so a queued message
 * is delivered even if the user has navigated away from the chat they wrote it
 * in.
 */
export function useOutboxFlush(): void {
  const client = useSynapseClient()
  const state = useConnectionState()
  const queryClient = useQueryClient()
  const selfId = useSessionStore((session) => session.session?.userId ?? '')
  const applyChatMessage = useChatStore((store) => store.applyMessage)
  const upsertChat = useChatStore((store) => store.upsert)
  const flushing = useRef(false)

  useEffect(() => {
    if (state !== 'ready' || !selfId || flushing.current) return
    const queued = useOutboxStore.getState().items.filter((item) => item.status === 'pending')
    if (queued.length === 0) return

    flushing.current = true
    void (async () => {
      for (const item of queued) {
        try {
          const ack = await client.request<Wire.SendAck>(MsgType.SEND, toWireSend(item), {
            expect: MsgType.SEND_ACK,
          })
          const { chatId, messageId, chatSeq, timestamp } = ack.body
          if (chatId !== item.target) {
            updateHistory(queryClient, item.target, () => undefined)
            upsertChat({ id: chatId, kind: 'direct', title: item.target, handle: item.target })
          }
          updateHistory(queryClient, chatId, (data) =>
            upsertMessage(
              data,
              draftMessage({
                id: messageId,
                chatId,
                senderId: selfId,
                text: item.text,
                seq: chatSeq,
                timestamp,
                replyTo: item.replyTo ?? '',
                mediaRef: item.attachment?.mediaRef ?? '',
                attachment: item.attachment ?? null,
                outgoing: true,
                status: 'sent',
                dedupKey: item.dedupKey,
              }),
            ),
          )
          applyChatMessage(
            {
              messageId,
              chatId,
              senderId: selfId,
              chatSeq,
              text: item.text,
              timestamp,
              edited: false,
              deleted: false,
            } as Wire.NewMessage,
            selfId,
          )
          useOutboxStore.getState().remove(item.dedupKey)
        } catch (error) {
          const permanent = error instanceof ProtocolError && !error.retryable
          if (permanent) {
            useOutboxStore.getState().update(item.dedupKey, {
              status: 'failed',
              error: error instanceof Error ? error.message : 'send failed',
            })
          }
          // Anything else: leave it queued and stop — the connection is likely
          // gone again, and hammering it helps nobody.
          break
        }
      }
      flushing.current = false
    })()
  }, [state, selfId, client, queryClient, applyChatMessage, upsertChat])
}
