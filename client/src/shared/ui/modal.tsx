'use client'

import { useEffect, useRef, type ReactNode } from 'react'

import { cn } from '../lib/cn'

/**
 * Dialog built on <dialog>, so focus trapping, the top layer and Esc-to-close
 * come from the platform instead of being re-implemented (and re-broken) here.
 */
export function Modal({
  open,
  onClose,
  title,
  children,
  className,
}: {
  open: boolean
  onClose: () => void
  title: string
  children: ReactNode
  className?: string
}) {
  const ref = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onClick={(event) => {
        // A click that lands on the dialog element itself is a backdrop click:
        // the content sits in a child box, so it never matches.
        if (event.target === ref.current) onClose()
      }}
      className={cn(
        'bg-surface-raised text-ink m-auto w-[min(28rem,calc(100vw-2rem))] rounded-2xl p-0',
        'backdrop:bg-black/40 backdrop:backdrop-blur-sm',
        className,
      )}
    >
      <div className="border-line flex items-center justify-between border-b px-5 py-4">
        <h2 className="text-base font-semibold">{title}</h2>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close"
          className="text-ink-faint hover:bg-surface-hover hover:text-ink rounded-lg p-1 transition-colors"
        >
          <svg
            viewBox="0 0 20 20"
            className="size-5"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
          >
            <path d="M5 5l10 10M15 5L5 15" strokeLinecap="round" />
          </svg>
        </button>
      </div>
      <div className="px-5 py-4">{children}</div>
    </dialog>
  )
}
