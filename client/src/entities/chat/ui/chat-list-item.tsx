'use client'

import Link from 'next/link'

import { Avatar, Badge } from '@/shared/ui'
import { useLocale, useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { formatListTimestamp } from '@/shared/lib/format'

import { isArchived, isMuted, isPinned, unreadCount, type ChatSummary } from '../model/types'

function KindIcon({ kind }: { kind: ChatSummary['kind'] }) {
  if (kind === 'direct') return null

  /*
   * The lock is the ONLY thing that distinguishes a secret chat in this list, and
   * that is the design rather than a shortcut. A secret chat is a chat: it has a
   * title, a preview, an unread count and a position in the ordering, and a row that
   * looked like a different kind of object is what made the old modal-beside-the-app
   * version something nobody opened twice.
   */
  if (kind === 'secret') {
    return (
      <svg
        viewBox="0 0 16 16"
        className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400"
        fill="currentColor"
        role="img"
      >
        <path d="M8 1a3.2 3.2 0 00-3.2 3.2V6H4a1 1 0 00-1 1v6a1 1 0 001 1h8a1 1 0 001-1V7a1 1 0 00-1-1h-.8V4.2A3.2 3.2 0 008 1zm0 1.4a1.8 1.8 0 011.8 1.8V6H6.2V4.2A1.8 1.8 0 018 2.4z" />
      </svg>
    )
  }

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

function MutedIcon({ label }: { label: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      className="text-ink-faint size-3.5 shrink-0"
      fill="currentColor"
      role="img"
      aria-label={label}
    >
      <path d="M8 1.5a3.5 3.5 0 00-3.5 3.5v2.2L3 9.7V11h10V9.7l-1.5-2.5V5A3.5 3.5 0 008 1.5zM6.4 12a1.6 1.6 0 003.2 0H6.4zM2.3 2.3l11.4 11.4-.9.9L1.4 3.2l.9-.9z" />
    </svg>
  )
}

function PinnedIcon({ label }: { label: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      className="text-ink-faint size-3 shrink-0"
      fill="currentColor"
      role="img"
      aria-label={label}
    >
      <path d="M9.6 1.4l5 5-1.1 1.1-.7-.7-2.8 2.8.4 3.5-1.2 1.2-3-3-3 3L2 14.1l3-3-3-3 1.2-1.2 3.5.4L9.5 4.5l-.7-.7L9.6 1.4z" />
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
  /**
   * Rendered to the right of the row. Optional so the entity component stays free of
   * any feature: the flag actions live in the chat-list widget, which is what knows
   * how to talk to the server.
   */
  actions?: React.ReactNode
}

export function ChatListItem({
  chat,
  active,
  selfId,
  senderLabel,
  typingUserIds,
  actions,
}: ChatListItemProps) {
  const t = useTranslate()
  const locale = useLocale()
  const unread = unreadCount(chat)
  const last = chat.lastMessage
  const someoneTyping = (typingUserIds?.length ?? 0) > 0
  const muted = isMuted(chat)

  const preview = someoneTyping
    ? typingUserIds!.length > 1
      ? t('chat.typingMany')
      : t('chat.typing', { name: senderLabel(typingUserIds![0]) })
    : last
      ? last.deleted
        ? t('chat.deleted')
        : `${last.senderId === selfId ? `${t('chats.you')}: ` : chat.kind !== 'direct' && chat.kind !== 'secret' ? `${senderLabel(last.senderId)}: ` : ''}${last.text}`
      : t('chats.noMessages')

  return (
    // `group` so the actions button can stay hidden until the row is hovered or the
    // button itself is focused — a menu trigger on every row at all times is visual
    // noise, and one that appears only on hover is unreachable by keyboard.
    <div className="group/row relative flex items-center">
      <Link
        href={`/chats/${chat.id}`}
        aria-current={active ? 'page' : undefined}
        className={cn(
          'flex min-w-0 flex-1 items-center gap-3 rounded-xl px-3 py-2.5 transition-colors',
          active ? 'bg-accent/12 dark:bg-accent/20' : 'hover:bg-surface-hover',
        )}
      >
        <Avatar seed={chat.id} name={chat.title} />

        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5">
            <KindIcon kind={chat.kind} />
            <span className="text-ink truncate text-sm font-medium">{chat.title}</span>
            {muted && <MutedIcon label={t('chats.muted')} />}
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
            <span className="ml-auto flex shrink-0 items-center gap-1.5">
              {isPinned(chat) && !isArchived(chat) && <PinnedIcon label={t('chats.pinned')} />}
              {unread > 0 && (
                // Grey rather than accent when muted: the count is still worth
                // showing — a muted chat is not an ignored one — but it must not
                // compete with the chats that are actually asking for attention.
                <Badge className={cn('shrink-0', muted && 'bg-ink-faint/25 text-ink-muted')}>
                  {unread > 99 ? '99+' : unread}
                </Badge>
              )}
            </span>
          </span>
        </span>
      </Link>

      {actions && (
        <span className="absolute right-1 opacity-0 transition-opacity group-hover/row:opacity-100 focus-within:opacity-100">
          {actions}
        </span>
      )}
    </div>
  )
}
