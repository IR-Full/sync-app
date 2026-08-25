'use client'

import Image from 'next/image'
import { useState } from 'react'

import { cn } from '../lib/cn'

/**
 * Avatar: an uploaded picture when there is one, a deterministic monogram
 * otherwise.
 *
 * The monogram is not a placeholder for a missing feature — it is the fallback
 * for the many users who never set a picture, and for every id we know without
 * having fetched a profile. Its colour comes from a stable key (user or chat
 * id), so the same person is the same colour everywhere, which is what makes a
 * list scannable.
 *
 * A picture arrives as a signed, EXPIRING media URL, so a failed load falls
 * back to the monogram rather than leaving a hole.
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

/** Pixel sizes matching SIZES — `next/image` needs intrinsic dimensions. */
const PIXELS = {
  small: 32,
  medium: 40,
  large: 56,
} as const

export interface AvatarProps {
  /** stable identity for the colour — a user id or chat id */
  seed: string
  name: string
  size?: keyof typeof SIZES
  className?: string
  /** shows a presence dot when defined */
  online?: boolean
  /** resolved picture URL; falls back to the monogram when absent or broken */
  src?: string
  /**
   * Loads the picture immediately instead of when it scrolls into view. For the
   * one avatar a screen is built around — a profile header — lazy loading only
   * delays the largest thing on it; in a list of many, the default is right.
   */
  preload?: boolean
}

export function Avatar({
  seed,
  name,
  size = 'medium',
  className,
  online,
  src,
  preload,
}: AvatarProps) {
  const color = PALETTE[hash(seed || name) % PALETTE.length]
  const [broken, setBroken] = useState(false)
  const picture = src && !broken ? src : null

  return (
    <span className={cn('relative inline-flex shrink-0', className)}>
      {picture ? (
        // `unoptimized` is what the docs prescribe for a src that requires
        // authentication: this URL is a signed, expiring link to the media
        // service, so an optimizer cache would outlive the signature that makes
        // it fetchable and start serving 403s from behind our own CDN.
        <Image
          src={picture}
          alt=""
          width={PIXELS[size]}
          height={PIXELS[size]}
          unoptimized
          preload={preload}
          onError={() => setBroken(true)}
          className={cn('rounded-full object-cover', SIZES[size])}
        />
      ) : (
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
      )}
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
