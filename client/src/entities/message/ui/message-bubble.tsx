'use client'

import type { ReactNode } from 'react'

import { useLocale, useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { formatTime } from '@/shared/lib/format'

import type { ReactionTally } from '../model/reactions'
import type { ChatMessage } from '../model/types'

/** Tick marks mirroring the delivery states the protocol actually reports. */
function StatusMark({ message }: { message: ChatMessage }) {
  const t = useTranslate()

  if (message.status === 'pending') {
    return (
      <svg viewBox="0 0 16 16" className="size-3.5 opacity-70" fill="none" stroke="currentColor" strokeWidth="1.6">
        <circle cx="8" cy="8" r="6" />
        <path d="M8 5v3.2l2 1.2" strokeLinecap="round" />
      </svg>
    )
  }
  if (message.status === 'failed') {
    return (
      <span title={message.error ?? t('chat.failed')} className="text-danger">
        <svg viewBox="0 0 16 16" className="size-3.5" fill="none" stroke="currentColor" strokeWidth="1.8">
          <circle cx="8" cy="8" r="6" />
          <path d="M8 4.8v4M8 11.2h.01" strokeLinecap="round" />
        </svg>
      </span>
    )
  }
  return (
    <svg viewBox="0 0 16 16" className="size-3.5 opacity-80" fill="none" stroke="currentColor" strokeWidth="1.8">
      <path d="M2.5 8.5l3 3 7-7" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

export interface MessageBubbleProps {
  message: ChatMessage
  /** shown above the text in group chats */
  senderLabel?: string
  showSender?: boolean
  /** true when the peer's read cursor has passed this message */
  readByPeer?: boolean
  onRetry?: (message: ChatMessage) => void
  /** rendered inside the bubble (attachment, poll) */
  children?: ReactNode
  reactions?: ReactionTally
  onToggleReaction?: (emoji: string) => void
  /** hover actions (edit, delete, reply, …) */
  actions?: ReactNode
  pinned?: boolean
  /** label of the original sender for a forwarded message */
  forwardedFrom?: string
  onOpenThread?: () => void
}

export function MessageBubble({
  message,
  senderLabel,
  showSender,
  readByPeer,
  onRetry,
  children,
  reactions,
  onToggleReaction,
  actions,
  pinned,
  forwardedFrom,
  onOpenThread,
}: MessageBubbleProps) {
  const t = useTranslate()
  const locale = useLocale()
  const own = message.outgoing

  if (message.deleted) {
    return (
      <div className={cn('flex px-1', own ? 'justify-end' : 'justify-start')}>
        <div className="max-w-[min(34rem,78%)] rounded-2xl border border-line px-3 py-2 text-sm text-ink-faint italic">
          {t('chat.deleted')}
        </div>
      </div>
    )
  }

  const tallies = Object.entries(reactions?.counts ?? {}).filter(([, count]) => count > 0)

  return (
    <div className={cn('group flex px-1', own ? 'justify-end' : 'justify-start')}>
      {/* Actions sit outside the bubble on the side the message came from. */}
      {own && actions && (
        <div className="mr-1 self-center opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          {actions}
        </div>
      )}

      <div
        className={cn(
          'max-w-[min(34rem,78%)] rounded-2xl px-3 py-2 text-sm',
          own
            ? 'rounded-br-md bg-bubble-own text-bubble-own-ink'
            : 'rounded-bl-md bg-bubble-peer text-bubble-peer-ink',
          message.status === 'failed' && 'ring-1 ring-danger',
        )}
      >
        {showSender && senderLabel && (
          <p className={cn('mb-0.5 text-xs font-semibold', own ? 'opacity-80' : 'text-accent')}>
            {senderLabel}
          </p>
        )}

        {forwardedFrom && (
          <p className="mb-1 border-l-2 border-current/40 pl-2 text-xs opacity-75">
            {t('chat.forwardedFrom', { name: forwardedFrom })}
          </p>
        )}

        {children && <div className="mb-1">{children}</div>}

        {message.text && <p className="break-words whitespace-pre-wrap">{message.text}</p>}

        {tallies.length > 0 && (
          <div className="mt-1.5 flex flex-wrap gap-1">
            {tallies.map(([emoji, count]) => (
              <button
                key={emoji}
                type="button"
                onClick={() => onToggleReaction?.(emoji)}
                className={cn(
                  'flex items-center gap-1 rounded-full px-1.5 py-0.5 text-xs transition-colors',
                  own ? 'bg-white/20 hover:bg-white/30' : 'bg-black/5 hover:bg-black/10 dark:bg-white/10',
                  reactions?.mine === emoji && 'ring-1 ring-current',
                )}
              >
                <span>{emoji}</span>
                <span className="tabular-nums">{count}</span>
              </button>
            ))}
          </div>
        )}

        <div
          className={cn(
            'mt-1 flex items-center justify-end gap-1 text-[11px]',
            own ? 'opacity-80' : 'text-ink-faint',
          )}
        >
          {message.replyCount > 0 && onOpenThread && (
            <button
              type="button"
              onClick={onOpenThread}
              className="mr-auto font-medium underline-offset-2 hover:underline"
            >
              {t('chat.replies', { count: message.replyCount })}
            </button>
          )}
          {pinned && (
            <span title={t('chat.pinned')} aria-label={t('chat.pinned')}>
              <svg viewBox="0 0 16 16" className="size-3" fill="currentColor">
                <path d="M9.5 1.5l5 5-1.4 1.4-.7-.7-2.8 2.8.7 3.5-1.4 1.4-3.2-3.2-3 3-.7-.7 3-3L1.3 7.8l1.4-1.4 3.5.7L9 4.3l-.7-.7 1.2-2.1z" />
              </svg>
            </span>
          )}
          {message.expiresAt > 0 && (
            <span title={t('chat.selfDestruct')} aria-label={t('chat.selfDestruct')}>
              <svg viewBox="0 0 16 16" className="size-3" fill="none" stroke="currentColor" strokeWidth="1.6">
                <circle cx="8" cy="9" r="5.5" />
                <path d="M8 6.5V9l1.6 1M6 1.5h4" strokeLinecap="round" />
              </svg>
            </span>
          )}
          {message.edited && <span className="italic">{t('chat.edited')}</span>}
          <time dateTime={new Date(message.timestamp).toISOString()}>
            {formatTime(message.timestamp, locale)}
          </time>
          {own && (
            <span className={cn('flex items-center', readByPeer && 'text-white')}>
              <StatusMark message={message} />
              {/* The second tick is the read receipt: READ_UPD from the other party. */}
              {readByPeer && message.status === 'sent' && (
                <svg
                  viewBox="0 0 16 16"
                  className="-ml-2 size-3.5"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.8"
                >
                  <path d="M2.5 8.5l3 3 7-7" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              )}
            </span>
          )}
        </div>

        {message.status === 'failed' && onRetry && (
          <button
            type="button"
            onClick={() => onRetry(message)}
            className="mt-1 text-xs font-medium underline underline-offset-2"
          >
            {t('chat.retry')}
          </button>
        )}
      </div>

      {!own && actions && (
        <div className="ml-1 self-center opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
          {actions}
        </div>
      )}
    </div>
  )
}
