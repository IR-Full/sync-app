'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { MsgType, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

export interface InviteLink {
  code: string
  chatId: string
  expiresAt: number
  maxUses: number
  uses: number
}

export type MemberRole = 'member' | 'admin' | 'owner'

const invitesKey = (chatId: string) => ['invites', chatId] as const

/**
 * A chat's invite links (admin only).
 *
 * Every membership operation answers with the same INVITES body — a list for a
 * query, an empty one for a command — so the reply type never distinguishes
 * them; the request does.
 */
export function useInviteLinks(chatId: string) {
  const client = useSynapseClient()
  const connected = useIsConnected()

  return useQuery({
    queryKey: invitesKey(chatId),
    enabled: connected && chatId.length > 0 && !chatId.startsWith('@'),
    staleTime: 30_000,
    retry: false,
    queryFn: async (): Promise<InviteLink[]> => {
      const reply = await client.request<Wire.Invites>(
        MsgType.INVITE_LIST,
        { chatId },
        { expect: MsgType.INVITES },
      )
      return reply.body.links.map((link) => ({
        code: link.code,
        chatId: link.chatId,
        expiresAt: link.expiresAt,
        maxUses: link.maxUses,
        uses: link.uses,
      }))
    },
  })
}

export function useCreateInvite(chatId: string) {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ maxUses, expiresAt }: { maxUses: number; expiresAt: number }) => {
      const reply = await client.request<Wire.Invites>(
        MsgType.INVITE_CREATE,
        { chatId, maxUses, expiresAt },
        { expect: MsgType.INVITES },
      )
      return reply.body.links[0] ?? null
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: invitesKey(chatId) }),
  })
}

export function useRevokeInvite(chatId: string) {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (code: string) => {
      await client.request<Wire.Invites>(
        MsgType.INVITE_REVOKE,
        { chatId, code },
        { expect: MsgType.INVITES },
      )
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: invitesKey(chatId) }),
  })
}

/** Promote or demote a member. Owner-only on the server. */
export function useSetMemberRole(chatId: string) {
  const client = useSynapseClient()

  return useMutation({
    mutationFn: async ({ userId, role }: { userId: string; role: MemberRole }) => {
      await client.request<Wire.Invites>(
        MsgType.SET_ROLE,
        { chatId, userId, role },
        { expect: MsgType.INVITES },
      )
    },
  })
}

/**
 * Claims or clears a chat's public handle (t.me/<name> style).
 *
 * An empty username clears it, turning the chat back into invite-only. The
 * handle is unique across all chats, so a taken name comes back as a conflict.
 */
export function useSetChatUsername(chatId: string) {
  const client = useSynapseClient()

  return useMutation({
    mutationFn: async (username: string) => {
      await client.request<Wire.Invites>(
        MsgType.SET_USERNAME,
        { chatId, username: username.trim().replace(/^@+/, '') },
        { expect: MsgType.INVITES },
      )
    },
  })
}

/**
 * Owner/admin export of a whole chat.
 *
 * Streams as pages of CHAT_EXPORT_RESULT; the final one carries `done`. Each
 * page repeats the chat metadata and members, so only the messages accumulate.
 */
export function useChatExport() {
  const client = useSynapseClient()

  return useMutation({
    mutationFn: async (chatId: string) => {
      const page = await client.requestStream<Wire.ChatExportResult, Wire.ChatExportResult>(
        MsgType.CHAT_EXPORT,
        { chatId },
        {
          itemType: MsgType.CHAT_EXPORT_RESULT,
          endType: MsgType.CHAT_EXPORT_RESULT,
          // Pages and terminator share a type; only `done` tells them apart.
          isTerminal: (body) => (body as Wire.ChatExportResult).done === true,
          timeoutMs: 60_000,
        },
      )
      // The FIRST frame is the header (metadata + members, no messages); the
      // middle frames carry message pages newest-first; the terminator carries
      // only `done`. So metadata comes from the head, never from the tail.
      const [header, ...rest] = page.items
      const messages = [header, ...rest]
        .flatMap((entry) => entry?.messages ?? [])
        .sort((a, b) => a.chatSeq - b.chatSeq)

      return {
        chatId: header?.chatId ?? '',
        type: header?.type ?? '',
        title: header?.title ?? '',
        ownerId: header?.ownerId ?? '',
        members: header?.members ?? [],
        messages,
      }
    },
  })
}
