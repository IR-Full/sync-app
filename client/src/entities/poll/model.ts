'use client'

import { create } from 'zustand'

import type { Wire } from '@/shared/api'

export interface PollOption {
  index: number
  text: string
  votes: number
}

export interface Poll {
  pollId: string
  chatId: string
  messageId: string
  question: string
  options: PollOption[]
  totalVotes: number
  multiChoice: boolean
  anonymous: boolean
  closed: boolean
  /** this user's own choices — the server fills it per recipient */
  myVotes: number[]
}

interface PollState {
  byId: Record<string, Poll>
  /** messageId -> pollId, so a poll message can find its tally */
  byMessage: Record<string, string>
  apply: (poll: Poll) => void
  clear: () => void
}

export function pollFromWire(state: Wire.PollState): Poll {
  return {
    pollId: state.pollId,
    chatId: state.chatId,
    messageId: state.messageId,
    question: state.question,
    options: state.options.map((option) => ({
      index: option.index,
      text: option.text,
      votes: option.votes,
    })),
    totalVotes: state.totalVotes,
    multiChoice: state.multiChoice,
    anonymous: state.anonymous,
    closed: state.closed,
    myVotes: state.myVotes ?? [],
  }
}

/**
 * Live poll tallies.
 *
 * Every create/vote/close answers with the full POLL_STATE and the server fans
 * the same body out to the chat, so the tally is replaced wholesale rather than
 * incremented locally — voters never drift apart.
 */
export const usePollStore = create<PollState>((set) => ({
  byId: {},
  byMessage: {},

  apply: (poll) =>
    set((state) => ({
      byId: { ...state.byId, [poll.pollId]: poll },
      byMessage: poll.messageId
        ? { ...state.byMessage, [poll.messageId]: poll.pollId }
        : state.byMessage,
    })),

  clear: () => set({ byId: {}, byMessage: {} }),
}))

export function applyWirePoll(state: Wire.PollState): void {
  usePollStore.getState().apply(pollFromWire(state))
}

export function usePollForMessage(messageId: string): Poll | undefined {
  return usePollStore((state) => {
    const pollId = state.byMessage[messageId]
    return pollId ? state.byId[pollId] : undefined
  })
}
