export type ChatKind = 'direct' | 'group' | 'channel'

/**
 * A chat as this client knows it.
 *
 * Assembled locally rather than fetched: the gateway has no "list my chats"
 * message (the `ListUserChats` store method exists in the Go code but no
 * envelope type reaches it), so the client accumulates chats from the events
 * that do carry a chat id — CHAT_INFO on create, the join reply, incoming NEW
 * frames, drafts, and search hits — and persists the result per user.
 */
export interface ChatSummary {
  id: string
  kind: ChatKind
  /** display title; for a direct chat this is the peer's @handle when we know it */
  title: string
  /** "@username" target for a direct chat — the only way to address one before it exists */
  handle?: string
  /** the other participant in a direct chat, when known */
  peerUserId?: string
  ownerId?: string
  /** highest per-chat sequence we have seen */
  lastSeq: number
  /** our own read cursor; the protocol can SET it (READ) but never reports it back */
  lastReadSeq: number
  lastMessage?: {
    messageId: string
    senderId: string
    text: string
    timestamp: number
    deleted: boolean
  }
  /** local ordering key for the chat list */
  updatedAt: number
  /** true until we have actually exchanged anything — a placeholder from contacts */
  provisional?: boolean
}

/**
 * Unread is derived, not counted.
 *
 * Keeping a counter in sync across live delivery, history backfill, multi-device
 * reads and reconnect replay is a losing game; `lastSeq - lastReadSeq` cannot
 * drift because both ends are server-assigned sequences. Sending a message also
 * advances our read cursor, so our own messages never show up as unread.
 */
export function unreadCount(chat: ChatSummary): number {
  return Math.max(0, chat.lastSeq - chat.lastReadSeq)
}

export function isDirect(chat: ChatSummary): boolean {
  return chat.kind === 'direct'
}
