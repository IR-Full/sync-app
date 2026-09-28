'use client'

import { create } from 'zustand'

import type { Wire } from '@/shared/api'
import { readStorage, StorageKeys, writeStorage } from '@/shared/lib/storage'

import { compareChats, is1to1, isArchived, type ChatKind, type ChatSummary } from './types'

interface ChatState {
  /** whose registry is loaded — chats are per-account, never shared across logins */
  ownerId: string | null
  chats: Record<string, ChatSummary>
  load: (ownerId: string) => void
  reset: () => void
  upsert: (chat: Partial<ChatSummary> & { id: string }) => void
  /** folds an incoming or backfilled message into the summary */
  applyMessage: (message: Wire.NewMessage, selfId: string) => void
  markRead: (chatId: string, upToSeq: number) => void
  remove: (chatId: string) => void
}

function storageKey(ownerId: string): string {
  return `${StorageKeys.chats}:${ownerId}`
}

function persist(ownerId: string | null, chats: Record<string, ChatSummary>): void {
  if (!ownerId) return
  writeStorage(storageKey(ownerId), chats)
}

const EMPTY: ChatSummary = {
  id: '',
  kind: 'direct',
  title: '',
  lastSeq: 0,
  lastReadSeq: 0,
  updatedAt: 0,
}

export const useChatStore = create<ChatState>((set, get) => ({
  ownerId: null,
  chats: {},

  load: (ownerId) => {
    const chats = readStorage<Record<string, ChatSummary>>(storageKey(ownerId), {})
    set({ ownerId, chats })
  },

  reset: () => set({ ownerId: null, chats: {} }),

  upsert: (patch) => {
    const { chats, ownerId } = get()
    const previous = chats[patch.id] ?? { ...EMPTY, id: patch.id }
    const next: ChatSummary = {
      ...previous,
      ...patch,
      // A chat that has seen real traffic must never be demoted back to a
      // placeholder by a later contact sync.
      provisional:
        patch.provisional === false ? false : (previous.provisional ?? patch.provisional),
      updatedAt: patch.updatedAt ?? previous.updatedAt ?? Date.now(),
    }
    const updated = { ...chats, [patch.id]: next }
    persist(ownerId, updated)
    set({ chats: updated })
  },

  applyMessage: (message, selfId) => {
    const { chats, ownerId } = get()
    const existing = chats[message.chatId]
    const isOwn = message.senderId === selfId

    const previous: ChatSummary = existing ?? {
      ...EMPTY,
      id: message.chatId,
      // Kind is unknowable from a message alone; direct is the safe default and
      // gets corrected by CHAT_INFO when the chat was created through us.
      kind: 'direct',
      title: message.chatId,
    }

    // History backfill replays older messages through this same path, so only a
    // strictly newer sequence may move the preview.
    const isNewer = message.chatSeq >= previous.lastSeq
    const next: ChatSummary = {
      ...previous,
      provisional: false,
      // In a two-party chat anyone who is not us IS the other party — the only
      // place the peer's id can be learned, since no message reports chat
      // membership. Presence and secret chats both need it.
      //
      // `is1to1` rather than `kind === 'direct'`: a secret chat is also two-party
      // and also titleless, so a row that missed its peer would have nothing to be
      // named after.
      peerUserId:
        previous.peerUserId ?? (is1to1(previous) && !isOwn ? message.senderId : undefined),
      lastSeq: Math.max(previous.lastSeq, message.chatSeq),
      // Our own message means we have obviously seen everything up to it.
      lastReadSeq: isOwn
        ? Math.max(previous.lastReadSeq, message.chatSeq)
        : previous.lastReadSeq,
      lastMessage: isNewer
        ? {
            messageId: message.messageId,
            senderId: message.senderId,
            text: message.text,
            timestamp: message.timestamp,
            deleted: message.deleted,
          }
        : previous.lastMessage,
      updatedAt: isNewer ? Math.max(previous.updatedAt, message.timestamp) : previous.updatedAt,
    }

    const updated = { ...chats, [message.chatId]: next }
    persist(ownerId, updated)
    set({ chats: updated })
  },

  markRead: (chatId, upToSeq) => {
    const { chats, ownerId } = get()
    const chat = chats[chatId]
    if (!chat || chat.lastReadSeq >= upToSeq) return
    const updated = { ...chats, [chatId]: { ...chat, lastReadSeq: upToSeq } }
    persist(ownerId, updated)
    set({ chats: updated })
  },

  remove: (chatId) => {
    const { chats, ownerId } = get()
    const updated = { ...chats }
    delete updated[chatId]
    persist(ownerId, updated)
    set({ chats: updated })
  },
}))

/**
 * Chats for the MAIN list: archived ones hidden, pinned first, newest activity next.
 *
 * Ordering moved into `compareChats` rather than staying inline, because it now has
 * three parts that have to agree with the server's: pinned before unpinned, then the
 * later of the two activity timestamps, then a stable tiebreak. Sorting on
 * `updatedAt` alone put a chat that was busy while the tab was closed at the bottom
 * of the list, because `updatedAt` is when THIS client last touched the row.
 */
export function selectOrderedChats(state: ChatState): ChatSummary[] {
  return Object.values(state.chats).filter((c) => !isArchived(c)).sort(compareChats)
}

/**
 * The archived pile.
 *
 * A separate selector rather than a flag on the one above, so a caller cannot show
 * the archive by accident — the whole point of archiving is that the row is out of
 * the way until it is asked for.
 */
export function selectArchivedChats(state: ChatState): ChatSummary[] {
  return Object.values(state.chats).filter(isArchived).sort(compareChats)
}

/**
 * Parses the server's chat type.
 *
 * `direct` is the fallback for an unrecognised value, and that is the safe
 * direction: a future type this build does not know renders as an ordinary chat
 * rather than disappearing from the list. It must NOT fall back to `secret` for the
 * same reason in reverse — a chat wrongly treated as secret would have its history
 * hidden and its sends refused.
 */
export function chatKindFromString(value: string): ChatKind {
  return value === 'group' || value === 'channel' || value === 'secret' ? value : 'direct'
}
