'use client'

import { useCallback } from 'react'

import { applyWirePoll } from '@/entities/poll'
import { MsgType, useSynapseClient, type Wire } from '@/shared/api'

/**
 * Creating and voting on polls.
 *
 * Each operation answers with the complete post-change POLL_STATE, so the
 * caller's own view updates without waiting for the fanout round trip; other
 * members get the same body pushed to them.
 */
export function usePollActions(chatId: string) {
  const client = useSynapseClient()

  const create = useCallback(
    async (question: string, options: string[], multiChoice: boolean, anonymous: boolean) => {
      const reply = await client.request<Wire.PollState>(
        MsgType.POLL_CREATE,
        {
          chatId,
          question: question.trim(),
          options: options.map((option) => option.trim()).filter(Boolean),
          multiChoice,
          anonymous,
        },
        { expect: MsgType.POLL_STATE },
      )
      applyWirePoll(reply.body)
      return reply.body
    },
    [client, chatId],
  )

  const vote = useCallback(
    async (pollId: string, option: number) => {
      const reply = await client.request<Wire.PollState>(
        MsgType.POLL_VOTE,
        { pollId, option },
        { expect: MsgType.POLL_STATE },
      )
      applyWirePoll(reply.body)
    },
    [client],
  )

  const close = useCallback(
    async (pollId: string) => {
      const reply = await client.request<Wire.PollState>(
        MsgType.POLL_CLOSE,
        { pollId },
        { expect: MsgType.POLL_STATE },
      )
      applyWirePoll(reply.body)
    },
    [client],
  )

  return { create, vote, close }
}
