'use client'

import { useSynapse } from '@/shared/api'
import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { selectPendingCount, useOutboxStore } from '@/features/send-message'

/**
 * Surfaces connection trouble, and only connection trouble.
 *
 * A healthy connection shows nothing — a permanent "connected" bar is noise.
 * Browser-level offline is reported separately from a dropped socket because the
 * two need different user expectations: one resolves when the network returns,
 * the other is already being retried with backoff.
 */
export function ConnectionBanner() {
  const t = useTranslate()
  const { state, online } = useSynapse()
  const queued = useOutboxStore(selectPendingCount)

  const problem = !online
    ? { tone: 'danger' as const, text: t('connection.offline') }
    : state === 'reconnecting'
      ? { tone: 'warning' as const, text: t('connection.reconnecting') }
      : state === 'connecting' || state === 'authenticating'
        ? { tone: 'muted' as const, text: t('connection.connecting') }
        : state === 'closed'
          ? { tone: 'danger' as const, text: t('connection.closed') }
          : null

  if (!problem) return null

  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        'flex items-center justify-center gap-2 px-4 py-1.5 text-xs font-medium',
        problem.tone === 'danger' && 'bg-danger text-white',
        problem.tone === 'warning' && 'bg-warning text-white',
        problem.tone === 'muted' && 'bg-surface-hover text-ink-muted',
      )}
    >
      {problem.tone !== 'muted' && (
        <span aria-hidden className="size-1.5 animate-pulse rounded-full bg-current" />
      )}
      <span>{problem.text}</span>
      {queued > 0 && <span>· {t('connection.queued', { count: queued })}</span>}
    </div>
  )
}
