'use client'

import Link from 'next/link'
import { useParams } from 'next/navigation'
import { useMemo, useState } from 'react'
import { useShallow } from 'zustand/shallow'

import { ChatListItem, selectOrderedChats, useChatStore, useTypingStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { labelForUser, useUserDirectory } from '@/entities/user'
import { useContacts } from '@/features/contacts'
import { NewChatDialog } from '@/features/create-chat'
import { useTranslate } from '@/shared/i18n'
import { Avatar, Button, EmptyState, TextField } from '@/shared/ui'

/**
 * The chat list.
 *
 * Fed by the local chat registry rather than a server call — the protocol has no
 * "list my chats" message. Contacts are shown alongside as a way to *start* a
 * conversation, since a direct chat only comes into being when the first message
 * is sent to a handle.
 */
export function ChatList() {
  const t = useTranslate()
  const params = useParams<{ chatId?: string }>()
  const activeChatId = params?.chatId ?? ''

  // selectOrderedChats sorts into a NEW array on every call; without a shallow
  // compare that is a fresh snapshot each render and useSyncExternalStore loops.
  const chats = useChatStore(useShallow(selectOrderedChats))
  const selfId = useSessionStore((state) => state.session?.userId ?? '')
  const directory = useUserDirectory((state) => state.users)
  const typing = useTypingStore((state) => state.typing)
  const { data: contacts } = useContacts()

  const [filter, setFilter] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)

  const senderLabel = useMemo(
    () => (userId: string) => labelForUser(directory[userId], userId),
    [directory],
  )

  const typingFor = useMemo(
    () => (chatId: string) => {
      const users = typing[chatId]
      if (!users) return []
      const now = Date.now()
      return Object.entries(users)
        .filter(([userId, expiresAt]) => expiresAt > now && userId !== selfId)
        .map(([userId]) => userId)
    },
    [typing, selfId],
  )

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase()
    if (!query) return chats
    return chats.filter((chat) => chat.title.toLowerCase().includes(query))
  }, [chats, filter])

  // Contacts we have no chat with yet — the practical entry point for a new
  // direct conversation.
  const startable = useMemo(() => {
    if (!contacts) return []
    const known = new Set(chats.map((chat) => chat.peerUserId).filter(Boolean))
    return contacts.filter((contact) => !contact.blocked && !known.has(contact.userId))
  }, [contacts, chats])

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 px-3 pt-3 pb-2">
        <TextField
          placeholder={t('chats.search')}
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          className="h-9"
        />
        <Button
          size="small"
          onClick={() => setDialogOpen(true)}
          aria-label={t('chats.new')}
          title={t('chats.new')}
          className="h-9 shrink-0 px-2.5"
        >
          <svg
            viewBox="0 0 20 20"
            className="size-4"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
          >
            <path d="M10 4.5v11M4.5 10h11" strokeLinecap="round" />
          </svg>
        </Button>
      </div>

      <nav className="flex-1 overflow-y-auto px-2 pb-3" aria-label={t('chats.title')}>
        {visible.length === 0 && startable.length === 0 ? (
          <EmptyState
            title={filter ? t('chats.noMatches') : t('chats.empty')}
            description={filter ? undefined : t('chats.emptyHint')}
            action={
              !filter && (
                <Button size="small" onClick={() => setDialogOpen(true)}>
                  {t('chats.new')}
                </Button>
              )
            }
          />
        ) : (
          <div className="flex flex-col gap-0.5">
            {visible.map((chat) => (
              <ChatListItem
                key={chat.id}
                chat={chat}
                active={chat.id === activeChatId}
                selfId={selfId}
                senderLabel={senderLabel}
                typingUserIds={typingFor(chat.id)}
              />
            ))}

            {startable.length > 0 && (
              <>
                <p className="text-ink-faint mt-4 px-3 pb-1 text-xs font-semibold tracking-wide uppercase">
                  {t('chats.contacts')}
                </p>
                {startable.map((contact) => {
                  const known = directory[contact.userId]
                  const handle = known?.username ?? contact.name
                  return (
                    <Link
                      key={contact.userId}
                      href={`/chats/new?to=${encodeURIComponent((handle || contact.userId).replace(/^@/, ''))}`}
                      className="hover:bg-surface-hover flex items-center gap-3 rounded-xl px-3 py-2 transition-colors"
                    >
                      <Avatar
                        seed={contact.userId}
                        name={contact.name || contact.userId}
                        size="small"
                      />
                      <span className="text-ink truncate text-sm">
                        {labelForUser(known, contact.userId)}
                      </span>
                    </Link>
                  )
                })}
              </>
            )}
          </div>
        )}
      </nav>

      <NewChatDialog open={dialogOpen} onClose={() => setDialogOpen(false)} />
    </div>
  )
}
