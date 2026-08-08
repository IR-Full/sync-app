'use client'

import { create } from 'zustand'

import { config } from '@/shared/config/env'

interface TypingState {
  /** chatId -> userId -> unix ms when the indicator should disappear */
  typing: Record<string, Record<string, number>>
  mark: (chatId: string, userId: string, active: boolean) => void
  sweep: () => void
  clear: () => void
}

/**
 * Who is typing, where.
 *
 * TYPING is an ephemeral, droppable frame: the gateway rate-limits it to about
 * one per chat every two seconds and silently discards the rest under load, and
 * there is no "stopped typing" guarantee — the active=false frame may never
 * arrive. So each indicator carries its own expiry and a sweep clears the
 * stragglers, instead of trusting the peer to turn it off.
 */
export const useTypingStore = create<TypingState>((set) => ({
  typing: {},

  mark: (chatId, userId, active) =>
    set((state) => {
      const forChat = { ...(state.typing[chatId] ?? {}) }
      if (active) {
        forChat[userId] = Date.now() + config.typingTimeoutMs
      } else {
        delete forChat[userId]
      }
      return { typing: { ...state.typing, [chatId]: forChat } }
    }),

  sweep: () =>
    set((state) => {
      const now = Date.now()
      let changed = false
      const next: Record<string, Record<string, number>> = {}
      for (const [chatId, users] of Object.entries(state.typing)) {
        const live: Record<string, number> = {}
        for (const [userId, expiry] of Object.entries(users)) {
          if (expiry > now) live[userId] = expiry
          else changed = true
        }
        if (Object.keys(live).length > 0) next[chatId] = live
      }
      return changed ? { typing: next } : state
    }),

  clear: () => set({ typing: {} }),
}))

const EMPTY: string[] = []

/** Ids of users currently typing in a chat, excluding ourselves. */
export function selectTypingUserIds(
  state: TypingState,
  chatId: string,
  selfId: string,
): string[] {
  const users = state.typing[chatId]
  if (!users) return EMPTY
  const now = Date.now()
  const ids = Object.entries(users)
    .filter(([userId, expiry]) => expiry > now && userId !== selfId)
    .map(([userId]) => userId)
  return ids.length > 0 ? ids : EMPTY
}
