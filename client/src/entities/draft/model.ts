'use client'

import { create } from 'zustand'

export interface Draft {
  chatId: string
  text: string
  replyTo: string
  updatedAt: number
}

interface DraftState {
  /** chatId -> draft, kept live so the composer can restore instantly */
  byChat: Record<string, Draft>
  merge: (drafts: Draft[]) => void
  clear: () => void
}

/**
 * Cross-device drafts.
 *
 * Private to one user but shared across their devices: DRAFT_SET is answered
 * with nothing, and the server instead mirrors a DRAFTS frame to that user's
 * *other* connections. Merging is last-write-wins on the server's timestamp,
 * which is the only clock both devices agree on.
 */
export const useDraftStore = create<DraftState>((set) => ({
  byChat: {},

  merge: (drafts) =>
    set((state) => {
      const byChat = { ...state.byChat }
      for (const draft of drafts) {
        const existing = byChat[draft.chatId]
        if (existing && existing.updatedAt > draft.updatedAt) continue
        if (draft.text) byChat[draft.chatId] = draft
        else delete byChat[draft.chatId]
      }
      return { byChat }
    }),

  clear: () => set({ byChat: {} }),
}))

export function useDraft(chatId: string): string {
  return useDraftStore((state) => state.byChat[chatId]?.text ?? '')
}
