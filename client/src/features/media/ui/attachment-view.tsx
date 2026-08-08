'use client'

import type { MessageAttachment } from '@/entities/message'
import { cn } from '@/shared/lib/cn'
import { Spinner } from '@/shared/ui'

import { useMediaUrl } from '../model/use-media'

function formatSize(bytes: number): string {
  if (!bytes) return ''
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(value < 10 && unit > 0 ? 1 : 0)} ${units[unit]}`
}

/**
 * Renders an attached blob.
 *
 * The download URL is fetched lazily and per ref: the server issues short-lived
 * signed links (MEDIA_FETCH → MEDIA_URL), so a URL baked into the message at
 * send time would be dead by the time anyone scrolled back to it.
 */
export function AttachmentView({
  attachment,
  own,
}: {
  attachment: MessageAttachment
  own: boolean
}) {
  const { data: url, isLoading, isError } = useMediaUrl(attachment.mediaRef)

  if (isError) {
    return (
      <div className="rounded-xl border border-dashed border-current/40 px-3 py-2 text-xs opacity-70">
        {attachment.filename || attachment.mediaRef}
      </div>
    )
  }

  if (isLoading || !url) {
    return (
      <div className="flex h-24 items-center justify-center rounded-xl bg-black/5 dark:bg-white/5">
        <Spinner className="size-4" />
      </div>
    )
  }

  if (attachment.kind === 'image') {
    // A plain <img>, not next/image: the src is a short-lived signed URL on a
    // proxied path, and the optimiser would cache it well past its expiry.
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={url}
        alt={attachment.filename || 'image'}
        width={attachment.width || undefined}
        height={attachment.height || undefined}
        className="max-h-80 w-auto max-w-full rounded-xl object-cover"
        loading="lazy"
      />
    )
  }

  if (attachment.kind === 'video') {
    return <video src={url} controls className="max-h-80 max-w-full rounded-xl" />
  }

  if (attachment.kind === 'voice') {
    return <audio src={url} controls className="w-64 max-w-full" />
  }

  return (
    <a
      href={url}
      download={attachment.filename || undefined}
      className={cn(
        'flex items-center gap-2 rounded-xl px-3 py-2 text-xs transition-colors',
        own ? 'bg-white/15 hover:bg-white/25' : 'bg-black/5 hover:bg-black/10 dark:bg-white/10',
      )}
    >
      <svg
        viewBox="0 0 20 20"
        className="size-5 shrink-0"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
      >
        <path
          d="M11 2.5H6a1.5 1.5 0 00-1.5 1.5v12A1.5 1.5 0 006 17.5h8a1.5 1.5 0 001.5-1.5V7L11 2.5z"
          strokeLinejoin="round"
        />
        <path d="M11 2.5V7h4.5" strokeLinejoin="round" />
      </svg>
      <span className="min-w-0">
        <span className="block truncate font-medium">{attachment.filename || 'file'}</span>
        {attachment.size > 0 && (
          <span className="opacity-70">{formatSize(attachment.size)}</span>
        )}
      </span>
    </a>
  )
}
