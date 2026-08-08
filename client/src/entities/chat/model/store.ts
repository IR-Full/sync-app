'use client'

import { create } from 'zustand'

import type { Wire } from '@/shared/api'
import { readStorage, StorageKeys, writeStorage } from '@/shared/lib/storage'

import type { ChatKind, ChatSummary } from './types'

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
      // In a direct chat anyone who is not us IS the other party — the only
      // place the peer's id can be learned, since no message reports chat
      // membership. Presence and secret chats both need it.
      peerUserId:
        previous.peerUserId ??
        (previous.kind === 'direct' && !isOwn ? message.senderId : undefined),
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

/** Chats ordered newest-activity first. */
export function selectOrderedChats(state: ChatState): ChatSummary[] {
  return Object.values(state.chats).sort((a, b) => b.updatedAt - a.updatedAt)
}

export function chatKindFromString(value: string): ChatKind {
  return value === 'group' || value === 'channel' ? value : 'direct'
}
