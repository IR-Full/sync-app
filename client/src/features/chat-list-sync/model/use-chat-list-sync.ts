'use client'

import { useEffect, useRef } from 'react'

import { chatKindFromString, useChatStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { MsgType, useIsConnected, useSyncAppClient, type Wire } from '@/shared/api'

/** Server-side cap; asking for more just gets this many back. */
const PAGE_LIMIT = 100

/** Refuses to walk forever if the server ever returns a non-advancing cursor. */
const MAX_PAGES = 100

/**
 * Pulls the authoritative chat list on connect.
 *
 * Everything else in this client learns about a chat as a *consequence* of
 * traffic — an inbound NEW, a send ack, a CHAT_INFO from a create, a join reply,
 * a resolved handle. That is enough to keep the list current once it exists, and
 * useless for a browser that has never seen any of it: a fresh install, a cleared
 * localStorage or a second browser opened on the same account all start blank,
 * with no way to discover the conversations the account is already in.
 *
 * `CHAT_LIST` (123) → `CHATS` (124) is the one message that enumerates them, and it
 * now carries what a row actually needs: the last message, the unread count, the
 * activity timestamp and this account's mute/pin/archive. Before that a client had
 * to call HISTORY per chat to draw its own list, which moved the server's N+1 onto
 * the network.
 *
 * The locally-assembled registry stays — the two are complementary. The merge is an
 * upsert with MAXIMUMS on the sequence numbers, so a page computed a moment before a
 * NEW frame landed cannot walk a counter backwards.
 */
export function useChatListSync(): void {
  const client = useSyncAppClient()
  const connected = useIsConnected()
  const userId = useSessionStore((state) => state.session?.userId ?? '')
  /** guards against a second run for the same connection */
  const syncedFor = useRef<string | null>(null)

  useEffect(() => {
    if (!connected || !userId) {
      // Allow a resync after a drop: the account may have been added to a chat
      // from another device while this tab was offline.
      syncedFor.current = null
      return
    }
    if (syncedFor.current === userId) return
    syncedFor.current = userId

    let cancelled = false

    void (async () => {
      const upsert = useChatStore.getState().upsert
      let after = ''
      // The other half of the cursor. The list is ordered by ACTIVITY, which
      // reorders as messages arrive, so a cursor naming only a chat id would skip
      // and repeat rows exactly when the account is busy.
      let afterActivity = 0

      for (let page = 0; page < MAX_PAGES; page++) {
        let reply: { body: Wire.Chats }
        try {
          reply = await client.request<Wire.Chats>(
            MsgType.CHAT_LIST,
            { after, afterActivity, limit: PAGE_LIMIT },
            { expect: MsgType.CHATS },
          )
        } catch {
          // An older gateway does not know this type, and a transient failure is
          // not worth an error in the UI: the locally-assembled list still works,
          // and the next connect tries again.
          return
        }
        if (cancelled) return

        const known = useChatStore.getState().chats
        for (const chat of reply.body.chats) {
          if (!chat.chatId) continue
          upsert({
            id: chat.chatId,
            kind: chatKindFromString(chat.type),
            // A 1:1 chat has no server-side title; leaving it empty lets the
            // existing peer/handle fallback name the row as it already does.
            ...(chat.title ? { title: chat.title } : {}),
            ...(chat.username ? { handle: `@${chat.username}` } : {}),
            ...(chat.peerId ? { peerUserId: chat.peerId } : {}),
            ...(chat.ownerId ? { ownerId: chat.ownerId } : {}),
            // `upsert` merges shallowly, so a page computed a moment before a NEW
            // frame landed would otherwise walk the counter backwards — and unread
            // is derived from it, so that shows up as a badge that un-counts itself.
            lastSeq: Math.max(chat.lastSeq, known[chat.chatId]?.lastSeq ?? 0),
            /*
             * The read cursor, seeded from the server's unread count.
             *
             * This is the one thing the local derivation cannot do. Unread is
             * `lastSeq - lastReadSeq`, which is self-correcting and needs no counter
             * — but a browser that has never seen this account starts with
             * `lastReadSeq: 0`, so every chat shows every message as unread even
             * though the user read them on their phone an hour ago. The server knows
             * the real cursor; `lastSeq - unreadCount` recovers it.
             *
             * Taken as a MAXIMUM against what is already known, because a page
             * computed a moment ago must not walk a cursor backwards that a READ
             * from this tab has since advanced.
             */
            lastReadSeq: Math.max(
              known[chat.chatId]?.lastReadSeq ?? 0,
              Math.max(0, chat.lastSeq - (chat.unreadCount ?? 0)),
            ),
            // The server's activity timestamp: what the list sorts on, and the other
            // half of the paging cursor.
            ...(chat.lastActivityAt ? { lastActivityAt: chat.lastActivityAt } : {}),
            // This account's own settings. Until the server grew a message for them
            // they were unreachable, so a client had no way to show a muted chat as
            // muted even though the column existed.
            flags: {
              ...(chat.mutedUntil ? { mutedUntil: chat.mutedUntil } : {}),
              ...(chat.pinned ? { pinned: true } : {}),
              ...(chat.archived ? { archived: true } : {}),
            },
            // The preview. A row could not be drawn without calling HISTORY per chat
            // before the server started sending this, which turned the list into N+1
            // round trips.
            ...(chat.lastMessage
              ? {
                  lastMessage: {
                    messageId: chat.lastMessage.messageId,
                    senderId: chat.lastMessage.senderId,
                    text: chat.lastMessage.text,
                    timestamp: chat.lastMessage.timestamp,
                    deleted: chat.lastMessage.deleted,
                  },
                }
              : {}),
            // This chat demonstrably exists server-side, so it is no longer a
            // placeholder built from a contact.
            provisional: false,
          })
        }

        if (reply.body.done || !reply.body.nextAfter || reply.body.nextAfter === after) return
        after = reply.body.nextAfter
        afterActivity = reply.body.nextAfterActivity ?? 0
      }
    })()

    return () => {
      cancelled = true
    }
  }, [client, connected, userId])
}
