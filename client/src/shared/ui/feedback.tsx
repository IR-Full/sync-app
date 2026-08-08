'use client'

import type { ReactNode } from 'react'

import { cn } from '../lib/cn'

export function Spinner({ className }: { className?: string }) {
  return (
    <span
      role="status"
      aria-live="polite"
      className={cn(
        'border-ink-faint inline-block size-5 animate-spin rounded-full border-2 border-t-transparent',
        className,
      )}
    />
  )
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: {
  icon?: ReactNode
  title: string
  description?: string
  action?: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-2 px-6 py-12 text-center',
        className,
      )}
    >
      {icon && <div className="text-ink-faint mb-1">{icon}</div>}
      <p className="text-ink font-medium">{title}</p>
      {description && <p className="text-ink-muted max-w-xs text-sm">{description}</p>}
      {action && <div className="mt-3">{action}</div>}
    </div>
  )
}

export function Badge({
  children,
  tone = 'accent',
  className,
}: {
  children: ReactNode
  tone?: 'accent' | 'muted' | 'danger'
  className?: string
}) {
  const tones = {
    accent: 'bg-accent text-accent-ink',
    muted: 'bg-surface-hover text-ink-muted',
    danger: 'bg-danger text-white',
  }
  return (
    <span
      className={cn(
        'inline-flex min-w-5 items-center justify-center rounded-full px-1.5 py-0.5 text-xs font-semibold tabular-nums',
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  )
}

export function ErrorNote({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <p role="alert" className={cn('text-danger text-sm', className)}>
      {children}
    </p>
  )
}
