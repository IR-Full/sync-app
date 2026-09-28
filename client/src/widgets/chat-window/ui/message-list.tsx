'use client'

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'

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

/**
 * How many messages are mounted when a chat opens.
 *
 * The cache can hold thousands for a busy conversation, and every one of them
 * used to become a DOM subtree the instant the chat was opened — bubble,
 * reactions, avatar, action menu — for messages nobody was going to scroll to.
 * Opening a long chat cost a layout pass over the entire history.
 *
 * Two screens' worth is enough that the first scroll gesture never runs out of
 * content, which is the only thing the number has to guarantee.
 */
const INITIAL_WINDOW = 80

/** How many more are mounted each time the window is grown. */
const WINDOW_STEP = 80

/**
 * What this is NOT: true virtualization. Rows are never UNMOUNTED, only mounted
 * late. Recycling variable-height bubbles would mean measuring each one and
 * maintaining a scroll-offset map, and getting that wrong shows up as the
 * viewport jumping while someone is reading — the exact failure the anchoring
 * below exists to prevent. Bounding the initial mount removes the cost that
 * actually hurts (opening a chat) without putting the reading experience at
 * risk; the unbounded case that remains is a user who has deliberately scrolled
 * through thousands of messages in one sitting.
 */

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

  /**
   * How many messages the transcript held when this view first mounted.
   *
   * A lazy initializer, so it is captured once and never changes. The window is
   * derived from it, which is what keeps a row from ever being UNMOUNTED: any
   * message arriving after mount widens the window by exactly one, so the set
   * already on screen stays on screen. Unmounting from the top would shift
   * everything below it upward while someone is reading — the same jolt the
   * prepend anchoring exists to prevent, reintroduced from the other end.
   */
  const [baseline] = useState(() => messages.length)

  /** Extra rows the reader has asked for by scrolling to the top. */
  const [requested, setRequested] = useState(0)
  /** scrollHeight captured before an older page is prepended */
  const anchor = useRef<number | null>(null)
  const lastCount = useRef(0)

  // Slicing from the END is what makes this safe: the newest message is always
  // mounted, so an arriving one is never hidden, and the window only ever grows
  // — upward, which is the same shape as a prepended page and is already
  // handled by the anchoring below.
  const windowSize = INITIAL_WINDOW + requested + Math.max(0, messages.length - baseline)
  const visible =
    messages.length > windowSize ? messages.slice(messages.length - windowSize) : messages
  const windowHasMore = messages.length > windowSize

  // Prepending older messages would otherwise yank the viewport upward: restore
  // the previous distance from the bottom so the user keeps reading where they
  // were. Layout effect, because it must happen before the browser paints.
  useLayoutEffect(() => {
    const element = scroller.current
    if (!element) return

    if (anchor.current !== null) {
      element.scrollTop = element.scrollHeight - anchor.current
      anchor.current = null
      lastCount.current = visible.length
      return
    }

    const grew = visible.length > lastCount.current
    lastCount.current = visible.length
    if (grew && following.current) {
      element.scrollTop = element.scrollHeight
    }
    // Depends on the MOUNTED count: growing the window changes the scroll height
    // exactly as a prepended page does, and both need the same correction.
  }, [visible.length])

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

    if (element.scrollTop >= LOAD_MORE_THRESHOLD_PX) return

    // Reaching the top means "show me more", and there are two different places
    // "more" can come from. Messages already in the cache but not yet mounted
    // are free; a network page is not. Exhaust the local ones first, or a chat
    // with a thousand cached messages would fetch a new page while nine hundred
    // it already has sit unmounted.
    if (windowHasMore) {
      anchor.current = element.scrollHeight
      setRequested((current) => current + WINDOW_STEP)
      return
    }
    if (hasOlder && !loadingOlder) {
      anchor.current = element.scrollHeight
      onLoadOlder()
    }
  }

  if (messages.length === 0) {
    return (
      <div className="text-ink-muted flex flex-1 items-center justify-center px-6 text-center text-sm">
        {emptyLabel}
      </div>
    )
  }

  return (
    <div
      ref={scroller}
      onScroll={onScroll}
      // role="log" carries an implicit aria-live="polite" and
      // aria-relevant="additions", so a screen reader announces messages as they
      // arrive but stays quiet when an older page is prepended — which is the
      // distinction that makes the announcement useful rather than a flood.
      role="log"
      aria-label={t('chat.transcript')}
      // Scrolling a region is otherwise mouse-only: without a tab stop a keyboard
      // user cannot reach the history at all, and reaching it is also what lets
      // the scroll handler load older pages for them.
      tabIndex={0}
      className="flex flex-1 flex-col gap-1 overflow-y-auto px-3 py-4"
    >
      {loadingOlder && (
        <div className="flex justify-center py-2">
          <Spinner className="size-4" />
        </div>
      )}
      {!hasOlder && !loadingOlder && !windowHasMore && (
        <p className="text-ink-faint py-2 text-center text-xs">{t('chat.historyStart')}</p>
      )}

      {visible.map((message, index) => {
        const previous = visible[index - 1]
        const startsNewDay = !previous || isDifferentDay(previous.timestamp, message.timestamp)
        // Only label the first message of a run from the same sender.
        const startsRun = !previous || previous.senderId !== message.senderId || startsNewDay

        return (
          <div key={message.id} className="contents">
            {startsNewDay && (
              <div className="my-3 flex justify-center">
                <span className="bg-surface-hover text-ink-muted rounded-full px-3 py-1 text-xs">
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
              forwardedFrom={
                message.forward ? senderLabel(message.forward.senderId) : undefined
              }
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
