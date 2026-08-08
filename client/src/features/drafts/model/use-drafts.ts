'use client'

import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef } from 'react'

import { useDraftStore, type Draft } from '@/entities/draft'
import { MsgType, queryKeys, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

/**
 * Pulls the drafts changed since the beginning and keeps the store in sync.
 *
 * DRAFT_SYNC is cursor-based, but as with contacts this asks for everything: the
 * set is per-user and tiny, and a full snapshot removes any chance of a drifted
 * cursor silently hiding a draft.
 */
export function useDraftSync() {
  const client = useSynapseClient()
  const connected = useIsConnected()

  const query = useQuery({
    queryKey: queryKeys.drafts(),
    enabled: connected,
    staleTime: 60_000,
    queryFn: async (): Promise<Draft[]> => {
      const reply = await client.request<Wire.Drafts>(
        MsgType.DRAFT_SYNC,
        { since: 0 },
        { expect: MsgType.DRAFTS },
      )
      return reply.body.drafts.map((draft) => ({
        chatId: draft.chatId,
        text: draft.text,
        replyTo: draft.replyTo,
        updatedAt: draft.updatedAt,
      }))
    },
  })

  const drafts = query.data
  useEffect(() => {
    if (drafts) useDraftStore.getState().merge(drafts)
  }, [drafts])

  return query
}

/**
 * Saves a draft, debounced.
 *
 * Every keystroke would be a frame the server has to mirror to the user's other
 * devices, and DRAFT_SET counts as a state-changing message for flood control —
 * so the write waits for a pause in typing.
 */
export function useDraftWriter(chatId: string, delayMs = 800) {
  const client = useSynapseClient()
  const queryClient = useQueryClient()
  const connected = useIsConnected()
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastSaved = useRef<string | null>(null)

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  return useCallback(
    (text: string) => {
      // A handle target has no chat yet, and asking the server to resolve one
      // would create the chat just because someone typed into the box.
      if (!connected || !chatId || chatId.startsWith('@')) return
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => {
        if (lastSaved.current === text) return
        lastSaved.current = text
        try {
          client.send(MsgType.DRAFT_SET, { chatId, text, replyTo: '' })
          useDraftStore.getState().merge([{ chatId, text, replyTo: '', updatedAt: Date.now() }])
          void queryClient.invalidateQueries({ queryKey: queryKeys.drafts() })
        } catch {
          // A dropped draft is not worth surfacing; the next pause retries.
        }
      }, delayMs)
    },
    [client, chatId, connected, delayMs, queryClient],
  )
}
