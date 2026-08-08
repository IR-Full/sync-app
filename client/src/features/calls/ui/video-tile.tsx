'use client'

import { useEffect, useRef } from 'react'

import { cn } from '@/shared/lib/cn'

/**
 * One participant's media.
 *
 * A MediaStream cannot be handed to React as a prop value the way a URL can —
 * it has to be attached to the element imperatively, and re-attached whenever
 * the stream object changes.
 */
export function VideoTile({
  stream,
  label,
  muted = false,
  mirrored = false,
  audioOnly = false,
  className,
}: {
  stream: MediaStream | null
  label: string
  /** always true for our own tile, or we echo ourselves back */
  muted?: boolean
  mirrored?: boolean
  audioOnly?: boolean
  className?: string
}) {
  const video = useRef<HTMLVideoElement>(null)

  useEffect(() => {
    const element = video.current
    if (!element) return
    if (element.srcObject !== stream) element.srcObject = stream
    return () => {
      if (element.srcObject === stream) element.srcObject = null
    }
  }, [stream])

  const hasVideo =
    !audioOnly && (stream?.getVideoTracks().some((track) => track.enabled) ?? false)

  return (
    <div
      className={cn(
        'relative flex min-h-32 items-center justify-center overflow-hidden rounded-xl bg-black/80',
        className,
      )}
    >
      <video
        ref={video}
        autoPlay
        playsInline
        muted={muted}
        className={cn(
          'h-full w-full object-cover',
          !hasVideo && 'hidden',
          mirrored && 'scale-x-[-1]',
        )}
      />

      {!hasVideo && (
        <div className="flex flex-col items-center gap-2 text-white/70">
          <svg
            viewBox="0 0 24 24"
            className="size-8"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
          >
            <path d="M12 15a3 3 0 003-3V6a3 3 0 10-6 0v6a3 3 0 003 3z" />
            <path d="M18 11a6 6 0 01-12 0M12 17v4" strokeLinecap="round" />
          </svg>
        </div>
      )}

      <span className="absolute bottom-1.5 left-2 max-w-[80%] truncate rounded-md bg-black/50 px-1.5 py-0.5 text-[11px] text-white">
        {label}
      </span>
    </div>
  )
}
