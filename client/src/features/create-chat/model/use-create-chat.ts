'use client'

import { useMutation } from '@tanstack/react-query'

import { chatKindFromString, useChatStore } from '@/entities/chat'
import { MsgType, useSynapseClient, type Wire } from '@/shared/api'

export type GroupKind = 'group' | 'channel'

/**
 * Creates a group or a channel.
 *
 * CHAT_CREATE covers exactly these two kinds — the gateway rejects anything
 * else. Direct chats have no creation message at all: they come into existence
 * when the first message is addressed to "@username" (see `useDirectChatTarget`).
 * Members may be given as "@handles" or raw ids; the server resolves them and
 * fails the whole request if any one is unknown.
 */
export function useCreateGroupChat() {
  const client = useSynapseClient()
  const upsert = useChatStore((store) => store.upsert)

  return useMutation({
    mutationFn: async ({
      kind,
      title,
      members,
    }: {
      kind: GroupKind
      title: string
      members: string[]
    }) => {
      const reply = await client.request<Wire.ChatInfo>(
        MsgType.CHAT_CREATE,
        {
          type: kind,
          title: title.trim(),
          members: members
            .map((member) => member.trim())
            .filter(Boolean)
            .map((member) => (member.startsWith('@') ? member : `@${member}`)),
        },
        { expect: MsgType.CHAT_INFO },
      )
      return reply.body
    },
    onSuccess: (info) => {
      upsert({
        id: info.chatId,
        kind: chatKindFromString(info.type),
        title: info.title || info.chatId,
        ownerId: info.ownerId,
        provisional: false,
        updatedAt: Date.now(),
      })
    },
  })
}

/**
 * Joins a chat by invite code or public @handle.
 *
 * The reply carries only the joined chat's id — no title, no type — so the entry
 * added here stays sparse until a message or a CHAT_INFO fills it in.
 */
export function useJoinChat() {
  const client = useSynapseClient()
  const upsert = useChatStore((store) => store.upsert)

  return useMutation({
    mutationFn: async (codeOrHandle: string) => {
      const value = codeOrHandle.trim()
      const isHandle = value.startsWith('@')
      const reply = await client.request<Wire.Invites>(
        MsgType.JOIN,
        isHandle ? { handle: value.slice(1) } : { code: value },
        { expect: MsgType.INVITES },
      )
      return reply.body.joinedChat
    },
    onSuccess: (chatId) => {
      if (!chatId) return
      upsert({
        id: chatId,
        kind: 'group',
        title: chatId,
        provisional: false,
        updatedAt: Date.now(),
      })
    },
  })
}

/**
 * Normalises a username into the "@name" target that addresses a direct chat.
 *
 * There is nothing to call here: the chat is created server-side by the first
 * SEND to this target, so the UI just routes to a compose screen holding it.
 */
export function directChatTarget(username: string): string {
  const clean = username.trim().replace(/^@+/, '')
  return clean ? `@${clean}` : ''
}
