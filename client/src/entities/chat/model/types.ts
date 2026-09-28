/**
 * `secret` is a 1:1 conversation whose CONTENT never reaches the server in
 * readable form — a chat type now, not a side panel.
 *
 * It used to exist only as a relay with no chat row behind it, which is why this
 * client put secret chats in a modal window: with no row there was no list entry,
 * no title, no unread count and no settings to show. Now the ordinary screens work
 * and only the guarantees differ.
 */
export type ChatKind = 'direct' | 'group' | 'channel' | 'secret'

/**
 * A chat as this client knows it.
 *
 * Two sources, deliberately: `CHAT_LIST` (123) enumerates the account's chats on
 * every connect (`features/chat-list-sync`) and is authoritative for their type,
 * title, handle, owner and `lastSeq`; between connects the registry is kept
 * current from the events that carry a chat id — CHAT_INFO on create, the join
 * reply, incoming NEW frames, drafts, and search hits. The result is persisted
 * per user, so the list renders offline.
 *
 * The fields the enumeration does NOT carry — `lastMessage`, `lastReadSeq` — are
 * local by necessity: the server sends no preview, and it can set our read cursor
 * but never reports it back.
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
  /**
   * The server's own activity timestamp, and the paging cursor that goes with it.
   *
   * Distinct from `updatedAt`, which is whenever THIS client last touched the row:
   * a chat that was busy while the tab was closed has a server activity far ahead
   * of anything local. The list sorts on the later of the two, so a fresh browser
   * shows the same order as the phone that has been online all day.
   */
  lastActivityAt?: number
  /**
   * This account's own settings for the chat.
   *
   * They were unreachable until now: the server had a `muted` column from its first
   * migration with nothing reading it and no message to set it, so muting looked
   * supported and was impossible.
   */
  flags?: ChatFlags
  /** true until we have actually exchanged anything — a placeholder from contacts */
  provisional?: boolean
}

/** One account's private settings for a chat. */
export interface ChatFlags {
  /**
   * A DEADLINE in unix millis, not a boolean. "Mute for eight hours" is what muting
   * usually means and a flag cannot express it; a distant deadline expresses
   * "forever", so the deadline subsumes the flag. 0 or absent means not muted.
   */
  mutedUntil?: number
  pinned?: boolean
  archived?: boolean
}

/** Whether notifications are suppressed right now. */
export function isMuted(chat: ChatSummary, now = Date.now()): boolean {
  const until = chat.flags?.mutedUntil ?? 0
  return until > now
}

export function isPinned(chat: ChatSummary): boolean {
  return chat.flags?.pinned === true
}

export function isArchived(chat: ChatSummary): boolean {
  return chat.flags?.archived === true
}

export function isSecret(chat: ChatSummary): boolean {
  return chat.kind === 'secret'
}

/**
 * The chat list order: pinned first, then by activity, newest first.
 *
 * Activity is the LATER of the server's timestamp and this client's, because
 * neither alone is right. The server does not know about a draft we just typed;
 * this client does not know about the forty messages that arrived while the tab was
 * closed. Taking the maximum means a browser opened after a busy day shows the same
 * order as the phone that watched it happen.
 */
export function chatActivity(chat: ChatSummary): number {
  return Math.max(chat.updatedAt, chat.lastActivityAt ?? 0)
}

export function compareChats(a: ChatSummary, b: ChatSummary): number {
  const pinA = isPinned(a) ? 1 : 0
  const pinB = isPinned(b) ? 1 : 0
  if (pinA !== pinB) return pinB - pinA
  const actA = chatActivity(a)
  const actB = chatActivity(b)
  if (actA !== actB) return actB - actA
  // A stable tiebreak so the order does not shuffle between renders for chats that
  // have never been touched (both activity values zero on a fresh install).
  return a.id < b.id ? 1 : a.id > b.id ? -1 : 0
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

/**
 * Whether a chat is inherently two-party and therefore has no title of its own.
 *
 * Both direct and secret chats are, so the row has to be named after its peer. A
 * check written as `kind === 'direct'` is exactly the kind that silently omits the
 * type added later — which is why this is a function and not a comparison.
 */
export function is1to1(chat: ChatSummary): boolean {
  return chat.kind === 'direct' || chat.kind === 'secret'
}
