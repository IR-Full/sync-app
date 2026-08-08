'use client'

import type { Poll } from '@/entities/poll'
import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'

/**
 * A poll rendered inside its message bubble.
 *
 * Bars are drawn from the server's tally, which arrives complete on every
 * change — so a vote updates every viewer's percentages, not just the voter's.
 */
export function PollCard({
  poll,
  canClose,
  onVote,
  onClose,
}: {
  poll: Poll
  canClose: boolean
  onVote: (option: number) => void
  onClose: () => void
}) {
  const t = useTranslate()
  const total = Math.max(poll.totalVotes, 1)

  return (
    <div className="min-w-56">
      <p className="font-medium">{poll.question}</p>

      <div className="mt-2 flex flex-col gap-1.5">
        {poll.options.map((option) => {
          const mine = poll.myVotes.includes(option.index)
          const share = Math.round((option.votes / total) * 100)
          return (
            <button
              key={option.index}
              type="button"
              disabled={poll.closed}
              onClick={() => onVote(option.index)}
              className={cn(
                'relative overflow-hidden rounded-lg border px-2 py-1.5 text-left text-xs transition-colors',
                mine ? 'border-current' : 'border-current/25',
                !poll.closed && 'hover:border-current/60',
                poll.closed && 'cursor-default',
              )}
            >
              <span
                aria-hidden
                className="absolute inset-y-0 left-0 bg-current/15"
                style={{ width: `${poll.totalVotes ? share : 0}%` }}
              />
              <span className="relative flex items-center justify-between gap-2">
                <span className="truncate">
                  {mine ? '● ' : '○ '}
                  {option.text}
                </span>
                <span className="shrink-0 tabular-nums opacity-80">
                  {poll.totalVotes ? `${share}%` : '—'}
                </span>
              </span>
            </button>
          )
        })}
      </div>

      <div className="mt-1.5 flex items-center justify-between text-[11px] opacity-80">
        <span>{t('poll.votes', { count: poll.totalVotes })}</span>
        {poll.closed ? (
          <span>{t('poll.closed')}</span>
        ) : canClose ? (
          <button type="button" onClick={onClose} className="underline underline-offset-2">
            {t('poll.close')}
          </button>
        ) : null}
      </div>
    </div>
  )
}
