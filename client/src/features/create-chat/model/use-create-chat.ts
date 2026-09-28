'use client'

import { useMutation } from '@tanstack/react-query'

import { chatKindFromString, useChatStore } from '@/entities/chat'
import { MsgType, useSyncAppClient, type Wire } from '@/shared/api'

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
  const client = useSyncAppClient()
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
 * Creates a SECRET chat — a chat type, chosen at creation, alongside direct, group and
 * channel.
 *
 * That is the whole change in one function. Secret chats used to exist only as a relay
 * with no chat row behind them, which meant no entry in the list, no title, no unread
 * count, no mute setting and no history — so every client put them in a modal beside
 * the product, which is exactly what the data model said they were.
 *
 * Exactly one member, and the server enforces it: a secret chat with nobody has no
 * session to run, and one with several has no session they all share.
 *
 * The call is idempotent. The gateway returns the canonical row for the pair, so
 * tapping "secret chat" twice opens the same conversation rather than creating a second
 * — which is what makes it safe to call without first checking whether one exists.
 */
export function useCreateSecretChat() {
  const client = useSyncAppClient()
  const upsert = useChatStore((store) => store.upsert)

  return useMutation({
    mutationFn: async (peer: string) => {
      const target = peer.trim()
      const reply = await client.request<Wire.ChatInfo>(
        MsgType.CHAT_CREATE,
        {
          type: 'secret',
          // Titleless by construction: a two-party chat is named after its peer, and
          // the server ignores a title here rather than validating one.
          title: '',
          members: [target.startsWith('@') ? target : `@${target}`],
        },
        { expect: MsgType.CHAT_INFO },
      )
      return { info: reply.body, peer: target.replace(/^@/, '') }
    },
    onSuccess: ({ info, peer }) => {
      upsert({
        id: info.chatId,
        kind: chatKindFromString(info.type),
        // CHAT_INFO carries no peer — it answers with the chat that was made, not with
        // its membership — and a two-party chat with no title and no peer renders with
        // no name at all. The handle is the best label available until the next chat-list
        // enumeration supplies the resolved id.
        title: `@${peer}`,
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
  const client = useSyncAppClient()
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
