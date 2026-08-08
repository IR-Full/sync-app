'use client'

import { useEffect, useLayoutEffect, useRef, type ReactNode } from 'react'

import {
  MessageBubble,
  useReactionStore,
  type ChatMessage,
  type ReactionTally,
} from '@/entities/message'
import { useLocale, useTranslate } from '@/shared/i18n'
import { formatDateSeparator, isDifferentDay } from '@/shared/lib/format'
import { Spinner } from '@/shared/ui'

export interface MessageListProps {
  messages: ChatMessage[]
  showSenders: boolean
  senderLabel: (userId: string) => string
  peerReadSeq: number
  hasOlder: boolean
  loadingOlder: boolean
  onLoadOlder: () => void
  /** highest sequence currently visible — drives the read receipt */
  onVisibleSeq: (seq: number) => void
  onRetry?: (message: ChatMessage) => void
  onToggleReaction?: (message: ChatMessage, emoji: string) => void
  /** extra content rendered inside a bubble (attachment, poll) */
  renderExtras?: (message: ChatMessage) => ReactNode
  /** hover actions for a message */
  renderActions?: (message: ChatMessage) => ReactNode
  isPinned?: (messageId: string) => boolean
  onOpenThread?: (message: ChatMessage) => void
  emptyLabel: string
}

/** Distance from the bottom within which we keep following new messages. */
const FOLLOW_THRESHOLD_PX = 120
/** Distance from the top that triggers loading the previous page. */
const LOAD_MORE_THRESHOLD_PX = 200

const NO_REACTIONS: ReactionTally = { counts: {}, mine: null }

export function MessageList({
  messages,
  showSenders,
  senderLabel,
  peerReadSeq,
  hasOlder,
  loadingOlder,
  onLoadOlder,
  onVisibleSeq,
  onRetry,
  onToggleReaction,
  renderExtras,
  renderActions,
  isPinned,
  onOpenThread,
  emptyLabel,
}: MessageListProps) {
  const t = useTranslate()
  const locale = useLocale()
  const reactions = useReactionStore((state) => state.byMessage)
  const scroller = useRef<HTMLDivElement>(null)
  const following = useRef(true)
  /** scrollHeight captured before an older page is prepended */
  const anchor = useRef<number | null>(null)
  const lastCount = useRef(0)

  // Prepending older messages would otherwise yank the viewport upward: restore
  // the previous distance from the bottom so the user keeps reading where they
  // were. Layout effect, because it must happen before the browser paints.
  useLayoutEffect(() => {
    const element = scroller.current
    if (!element) return

    if (anchor.current !== null) {
      element.scrollTop = element.scrollHeight - anchor.current
      anchor.current = null
      lastCount.current = messages.length
      return
    }

    const grew = messages.length > lastCount.current
    lastCount.current = messages.length
    if (grew && following.current) {
      element.scrollTop = element.scrollHeight
    }
  }, [messages])

  // The newest message the user can actually see is what "read" means here.
  useEffect(() => {
    if (!following.current || messages.length === 0) return
    const newest = messages[messages.length - 1]
    if (newest.seq > 0) onVisibleSeq(newest.seq)
  }, [messages, onVisibleSeq])

  function onScroll() {
    const element = scroller.current
    if (!element) return
    const distanceFromBottom = element.scrollHeight - element.scrollTop - element.clientHeight
    following.current = distanceFromBottom < FOLLOW_THRESHOLD_PX

    if (following.current && messages.length > 0) {
      const newest = messages[messages.length - 1]
      if (newest.seq > 0) onVisibleSeq(newest.seq)
    }

    if (element.scrollTop < LOAD_MORE_THRESHOLD_PX && hasOlder && !loadingOlder) {
      anchor.current = element.scrollHeight
      onLoadOlder()
    }
  }

  if (messages.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center px-6 text-center text-sm text-ink-muted">
        {emptyLabel}
      </div>
    )
  }

  return (
    <div
      ref={scroller}
      onScroll={onScroll}
      className="flex flex-1 flex-col gap-1 overflow-y-auto px-3 py-4"
    >
      {loadingOlder && (
        <div className="flex justify-center py-2">
          <Spinner className="size-4" />
        </div>
      )}
      {!hasOlder && !loadingOlder && (
        <p className="py-2 text-center text-xs text-ink-faint">{t('chat.historyStart')}</p>
      )}

      {messages.map((message, index) => {
        const previous = messages[index - 1]
        const startsNewDay = !previous || isDifferentDay(previous.timestamp, message.timestamp)
        // Only label the first message of a run from the same sender.
        const startsRun = !previous || previous.senderId !== message.senderId || startsNewDay

        return (
          <div key={message.id} className="contents">
            {startsNewDay && (
              <div className="my-3 flex justify-center">
                <span className="rounded-full bg-surface-hover px-3 py-1 text-xs text-ink-muted">
                  {formatDateSeparator(message.timestamp, locale)}
                </span>
              </div>
            )}
            <MessageBubble
              message={message}
              showSender={showSenders && startsRun && !message.outgoing}
              senderLabel={senderLabel(message.senderId)}
              readByPeer={message.seq > 0 && message.seq <= peerReadSeq}
              onRetry={onRetry}
              reactions={reactions[message.id] ?? NO_REACTIONS}
              onToggleReaction={(emoji) => onToggleReaction?.(message, emoji)}
              actions={renderActions?.(message)}
              pinned={isPinned?.(message.id)}
              forwardedFrom={message.forward ? senderLabel(message.forward.senderId) : undefined}
              onOpenThread={onOpenThread ? () => onOpenThread(message) : undefined}
            >
              {renderExtras?.(message)}
            </MessageBubble>
          </div>
        )
      })}
    </div>
  )
}
