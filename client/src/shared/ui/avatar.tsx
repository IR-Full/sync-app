'use client'

import { cn } from '../lib/cn'

/**
 * Deterministic identicon-style avatar.
 *
 * The server's user model has no avatar field and the protocol has no message
 * to upload one, so an avatar here is derived from a stable key (user or chat
 * id) rather than fetched. Same key always yields the same colour, which is
 * what makes it useful for recognising people at a glance.
 */
const PALETTE = [
  'bg-[#e8564f]',
  'bg-[#e08a2e]',
  'bg-[#2fa36b]',
  'bg-[#2f8ee0]',
  'bg-[#7a5cd6]',
  'bg-[#d1497f]',
  'bg-[#1f9d94]',
  'bg-[#8a6d3b]',
]

function hash(value: string): number {
  let h = 2166136261
  for (let i = 0; i < value.length; i++) {
    h ^= value.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return Math.abs(h)
}

function initials(name: string): string {
  const clean = name.replace(/^@/, '').trim()
  if (!clean) return '?'
  const parts = clean.split(/[\s_.-]+/).filter(Boolean)
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase()
  return clean.slice(0, 2).toUpperCase()
}

const SIZES = {
  small: 'size-8 text-xs',
  medium: 'size-10 text-sm',
  large: 'size-14 text-lg',
} as const

export interface AvatarProps {
  /** stable identity for the colour — a user id or chat id */
  seed: string
  name: string
  size?: keyof typeof SIZES
  className?: string
  /** shows a presence dot when defined */
  online?: boolean
}

export function Avatar({ seed, name, size = 'medium', className, online }: AvatarProps) {
  const color = PALETTE[hash(seed || name) % PALETTE.length]
  return (
    <span className={cn('relative inline-flex shrink-0', className)}>
      <span
        aria-hidden
        className={cn(
          'flex items-center justify-center rounded-full font-semibold text-white select-none',
          color,
          SIZES[size],
        )}
      >
        {initials(name)}
      </span>
      {online !== undefined && (
        <span
          aria-hidden
          className={cn(
            'border-surface absolute right-0 bottom-0 size-3 rounded-full border-2',
            online ? 'bg-success' : 'bg-ink-faint',
          )}
        />
      )}
    </span>
  )
}
