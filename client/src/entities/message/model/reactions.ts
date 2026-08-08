'use client'

import { create } from 'zustand'

export interface ReactionTally {
  /** emoji -> how many people reacted with it */
  counts: Record<string, number>
  /** emoji this user picked, if any — the toggle state of their own button */
  mine: string | null
}

interface ReactionState {
  /** messageId -> tally */
  byMessage: Record<string, ReactionTally>
  apply: (messageId: string, counts: Record<string, number>, emoji: string, added: boolean, isSelf: boolean) => void
  clear: () => void
}

/**
 * Reaction tallies.
 *
 * The gateway sends the *post-change* counts with every REACT_UPD, so the tally
 * is never accumulated locally — it is replaced wholesale by what the server
 * says. Only "which emoji is mine" has to be tracked here, because the counts
 * map says how many reacted but not who.
 *
 * Session-scoped: history frames carry no reaction data, so this fills in as
 * updates arrive (and from the reply to our own toggle).
 */
export const useReactionStore = create<ReactionState>((set) => ({
  byMessage: {},

  apply: (messageId, counts, emoji, added, isSelf) =>
    set((state) => {
      const previous = state.byMessage[messageId]
      const mine = isSelf ? (added ? emoji : null) : (previous?.mine ?? null)
      return {
        byMessage: { ...state.byMessage, [messageId]: { counts, mine } },
      }
    }),

  clear: () => set({ byMessage: {} }),
}))

const EMPTY: ReactionTally = { counts: {}, mine: null }

export function selectReactions(state: ReactionState, messageId: string): ReactionTally {
  return state.byMessage[messageId] ?? EMPTY
}

/** Emoji offered in the picker — a small, deliberately boring set. */
export const QUICK_REACTIONS = ['👍', '❤️', '😂', '😮', '😢', '🔥'] as const
