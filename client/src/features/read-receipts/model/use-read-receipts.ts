'use client'

import { useCallback, useEffect, useRef } from 'react'

import { selectPeerReadSeq, useChatStore, useReceiptStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { MsgType, useConnectionState, useSynapseClient } from '@/shared/api'
import { useSettingsStore } from '@/entities/settings'

/**
 * Marks a chat read up to a sequence.
 *
 * READ is fire-and-forget (the gateway replies only on failure) and the server
 * never reports our own cursor back, so the local registry is updated
 * optimistically — it is the only copy this client will ever see.
 */
export function useMarkRead(chatId: string) {
  const client = useSynapseClient()
  const state = useConnectionState()
  const markRead = useChatStore((store) => store.markRead)
  const sendReceipts = useSettingsStore((settings) => settings.sendReadReceipts)
  const highestSent = useRef(0)

  return useCallback(
    (upToSeq: number) => {
      if (!chatId || upToSeq <= 0) return
      // Local cursor advances regardless: "read" is a fact about this user, and
      // suppressing the receipt should not make their own unread badge lie.
      markRead(chatId, upToSeq)
      if (!sendReceipts || state !== 'ready' || upToSeq <= highestSent.current) return
      highestSent.current = upToSeq
      try {
        client.send(MsgType.READ, { chatId, upToChatSeq: upToSeq })
      } catch {
        // Nothing to recover: the next read in this chat re-sends a higher cursor.
      }
    },
    [client, chatId, state, markRead, sendReceipts],
  )
}

/** Highest sequence any other member has read — drives the double tick. */
export function usePeerReadSeq(chatId: string): number {
  const selfId = useSessionStore((session) => session.session?.userId ?? '')
  return useReceiptStore((store) => selectPeerReadSeq(store, chatId, selfId))
}

/** Clears live receipt state when the connection is torn down. */
export function useReceiptReset(connected: boolean): void {
  useEffect(() => {
    if (!connected) useReceiptStore.getState().clear()
  }, [connected])
}
