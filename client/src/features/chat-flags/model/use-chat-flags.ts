'use client'

import { useCallback } from 'react'

import { useChatStore } from '@/entities/chat'
import { MsgType, useSyncAppClient, type Wire } from '@/shared/api'

/**
 * Mute, pin and archive — one account's private settings for a chat.
 *
 * None of this was reachable before. The server has had a `muted` column since its
 * first migration, the model carried it and the converters moved it back and forth,
 * and nothing ever read it: there was no message to set it and the notification path
 * never consulted it. So muting looked supported from every angle except the one
 * that mattered.
 *
 * The local store is updated from the server's ECHO rather than optimistically, and
 * that is deliberate for these three. They are cheap to set and their whole visible
 * effect is an absence — a muted chat simply does not buzz — so a client that
 * assumed success would show "muted" for a request that failed, and the user would
 * find out by being notified anyway. The round trip is a few milliseconds and buys
 * certainty about something the user cannot otherwise verify.
 */
export function useChatFlags(): {
  setMuted: (chatId: string, until: number) => Promise<void>
  muteFor: (chatId: string, ms: number) => Promise<void>
  unmute: (chatId: string) => Promise<void>
  setPinned: (chatId: string, pinned: boolean) => Promise<void>
  setArchived: (chatId: string, archived: boolean) => Promise<void>
} {
  const client = useSyncAppClient()

  /**
   * Sends the whole flag set, merged over what is currently known.
   *
   * CHAT_FLAGS replaces all three rather than patching one, which means a caller
   * changing the pin must not drop the mute. Reading the current row and merging is
   * the only way to express "change this one" over a replacing message — and doing
   * it here, once, is why no call site has to remember.
   */
  const apply = useCallback(
    async (chatId: string, patch: Partial<Wire.ChatFlags>) => {
      const current = useChatStore.getState().chats[chatId]?.flags ?? {}
      const body: Wire.ChatFlags = {
        chatId,
        mutedUntil: patch.mutedUntil ?? current.mutedUntil ?? 0,
        pinned: patch.pinned ?? current.pinned ?? false,
        archived: patch.archived ?? current.archived ?? false,
      }
      const reply = await client.request<Wire.ChatFlagsSet>(MsgType.CHAT_FLAGS, body, {
        expect: MsgType.CHAT_FLAGS_SET,
      })
      // The server's own values, not the ones we asked for: it normalises an expired
      // mute deadline to zero, and a client that stored its request would show a chat
      // as muted until something contradicted it.
      useChatStore.getState().upsert({
        id: chatId,
        flags: {
          ...(reply.body.mutedUntil ? { mutedUntil: reply.body.mutedUntil } : {}),
          ...(reply.body.pinned ? { pinned: true } : {}),
          ...(reply.body.archived ? { archived: true } : {}),
        },
      })
    },
    [client],
  )

  return {
    setMuted: useCallback((chatId, until) => apply(chatId, { mutedUntil: until }), [apply]),
    /**
     * Mute for a duration, which is what people actually want far more often than
     * "forever" — and the reason the protocol carries a deadline rather than a flag.
     */
    muteFor: useCallback(
      (chatId, ms) => apply(chatId, { mutedUntil: Date.now() + ms }),
      [apply],
    ),
    unmute: useCallback((chatId) => apply(chatId, { mutedUntil: 0 }), [apply]),
    setPinned: useCallback((chatId, pinned) => apply(chatId, { pinned }), [apply]),
    setArchived: useCallback((chatId, archived) => apply(chatId, { archived }), [apply]),
  }
}

// The mute presets used to live here, as `{ labelKey: string }` pairs naming four
// dictionary keys that did not exist — so the menu they were written for would have
// rendered raw key names. They now live next to that menu, typed as `TranslationKey`
// so a missing key is a compile error rather than a label nobody notices.
//
// The other reason to move them: "forever" was `4102444800000 - Date.now()`, evaluated
// once at MODULE LOAD, which drifts in a long-lived tab and lands just past the
// ceiling the server clamps to.
