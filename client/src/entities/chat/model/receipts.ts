'use client'

import { create } from 'zustand'

interface ReceiptState {
  /** chatId -> userId -> highest chat sequence that user has read */
  cursors: Record<string, Record<string, number>>
  apply: (chatId: string, userId: string, upToSeq: number) => void
  clear: () => void
}

/**
 * Other people's read cursors.
 *
 * READ_UPD arrives whenever a member marks a chat read. The protocol only ever
 * pushes *other* users' cursors — there is no message to query anyone's cursor,
 * including our own — so this is a live-session view that starts empty on every
 * connect and fills in as receipts arrive.
 */
export const useReceiptStore = create<ReceiptState>((set) => ({
  cursors: {},

  apply: (chatId, userId, upToSeq) =>
    set((state) => {
      const forChat = state.cursors[chatId] ?? {}
      if ((forChat[userId] ?? 0) >= upToSeq) return state
      return {
        cursors: { ...state.cursors, [chatId]: { ...forChat, [userId]: upToSeq } },
      }
    }),

  clear: () => set({ cursors: {} }),
}))

/**
 * Highest sequence read by anyone other than us in this chat — the point up to
 * which our outgoing messages can show a read tick.
 */
export function selectPeerReadSeq(state: ReceiptState, chatId: string, selfId: string): number {
  const forChat = state.cursors[chatId]
  if (!forChat) return 0
  let highest = 0
  for (const [userId, seq] of Object.entries(forChat)) {
    if (userId !== selfId && seq > highest) highest = seq
  }
  return highest
}
