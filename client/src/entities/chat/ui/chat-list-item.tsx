'use client'

import Link from 'next/link'

import { Avatar, Badge } from '@/shared/ui'
import { useLocale, useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { formatListTimestamp } from '@/shared/lib/format'

import { unreadCount, type ChatSummary } from '../model/types'

function KindIcon({ kind }: { kind: ChatSummary['kind'] }) {
  if (kind === 'direct') return null
  return (
    <svg viewBox="0 0 16 16" className="text-ink-faint size-3.5 shrink-0" fill="currentColor">
      {kind === 'channel' ? (
        <path d="M2 6.5v3h2l4 3V3.5l-4 3H2zm9.2-2.1a5 5 0 010 7.2l-.9-.9a3.7 3.7 0 000-5.4l.9-.9z" />
      ) : (
        <path d="M5.5 7a2 2 0 100-4 2 2 0 000 4zm5 0a2 2 0 100-4 2 2 0 000 4zM1.5 12c0-1.7 1.8-2.8 4-2.8s4 1.1 4 2.8v1h-8v-1zm9 1v-1c0-1-.4-1.9-1.1-2.5.6-.2 1.3-.3 2.1-.3 2.2 0 4 1.1 4 2.8v1h-5z" />
      )}
    </svg>
  )
}

export interface ChatListItemProps {
  chat: ChatSummary
  active: boolean
  selfId: string
  /** resolves a sender id to a readable name for the preview line */
  senderLabel: (userId: string) => string
  /** ids of users currently typing in this chat */
  typingUserIds?: string[]
}

export function ChatListItem({
  chat,
  active,
  selfId,
  senderLabel,
  typingUserIds,
}: ChatListItemProps) {
  const t = useTranslate()
  const locale = useLocale()
  const unread = unreadCount(chat)
  const last = chat.lastMessage
  const someoneTyping = (typingUserIds?.length ?? 0) > 0

  const preview = someoneTyping
    ? typingUserIds!.length > 1
      ? t('chat.typingMany')
      : t('chat.typing', { name: senderLabel(typingUserIds![0]) })
    : last
      ? last.deleted
        ? t('chat.deleted')
        : `${last.senderId === selfId ? `${t('chats.you')}: ` : chat.kind !== 'direct' ? `${senderLabel(last.senderId)}: ` : ''}${last.text}`
      : t('chats.noMessages')

  return (
    <Link
      href={`/chats/${chat.id}`}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'flex items-center gap-3 rounded-xl px-3 py-2.5 transition-colors',
        active ? 'bg-accent/12 dark:bg-accent/20' : 'hover:bg-surface-hover',
      )}
    >
      <Avatar seed={chat.id} name={chat.title} />

      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <KindIcon kind={chat.kind} />
          <span className="text-ink truncate text-sm font-medium">{chat.title}</span>
          {last && (
            <time className="text-ink-faint ml-auto shrink-0 text-[11px]">
              {formatListTimestamp(last.timestamp, locale)}
            </time>
          )}
        </span>
        <span className="mt-0.5 flex items-center gap-2">
          <span
            className={cn('truncate text-xs', someoneTyping ? 'text-accent' : 'text-ink-muted')}
          >
            {preview}
          </span>
          {unread > 0 && (
            <Badge className="ml-auto shrink-0">{unread > 99 ? '99+' : unread}</Badge>
          )}
        </span>
      </span>
    </Link>
  )
}
