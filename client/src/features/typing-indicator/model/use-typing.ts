'use client'

import { useCallback, useRef } from 'react'
import { useShallow } from 'zustand/shallow'

import { selectTypingUserIds, useTypingStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { MsgType, useConnectionState, useSynapseClient } from '@/shared/api'
import { config } from '@/shared/config/env'

/**
 * Outgoing typing notifications, throttled to match the server's own limit.
 *
 * The gateway allows roughly one TYPING frame per chat every two seconds and
 * silently drops the rest, so sending on every keystroke would burn the whole
 * budget and get most frames discarded. Throttling here keeps the indicator
 * alive on the receiving side with the fewest frames that will actually pass.
 */
export function useTypingNotifier(chatId: string) {
  const client = useSynapseClient()
  const state = useConnectionState()
  const lastSentAt = useRef(0)

  return useCallback(
    (active: boolean) => {
      if (state !== 'ready' || !chatId) return
      const now = Date.now()
      if (active && now - lastSentAt.current < config.typingThrottleMs) return
      lastSentAt.current = active ? now : 0
      try {
        // Fire-and-forget: the gateway answers TYPING only by relaying it.
        client.send(MsgType.TYPING, { chatId, active })
      } catch {
        // A dropped indicator is never worth surfacing.
      }
    },
    [client, chatId, state],
  )
}

/** Ids of other users currently typing in a chat. */
export function useTypingUsers(chatId: string): string[] {
  const selfId = useSessionStore((session) => session.session?.userId ?? '')
  return useTypingStore(useShallow((store) => selectTypingUserIds(store, chatId, selfId)))
}
