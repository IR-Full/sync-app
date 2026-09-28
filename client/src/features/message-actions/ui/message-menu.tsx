'use client'

import { useEffect, useRef, useState } from 'react'

import { QUICK_REACTIONS, type ChatMessage } from '@/entities/message'
import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'

export interface MessageMenuProps {
  message: ChatMessage
  canEdit: boolean
  canDelete: boolean
  pinned: boolean
  onReact: (emoji: string) => void
  onEdit: () => void
  onDelete: () => void
  onReply: () => void
  onForward: () => void
  onTogglePin: () => void
  onOpenThread: () => void
}

/**
 * Per-message actions.
 *
 * Only offered for messages the server will actually accept them on: a pending
 * message has no id yet, and edit/delete are the author's alone (the gateway
 * enforces that too — this just avoids showing a button that would come back
 * `forbidden`).
 */
export function MessageMenu({
  message,
  canEdit,
  canDelete,
  pinned,
  onReact,
  onEdit,
  onDelete,
  onReply,
  onForward,
  onTogglePin,
  onOpenThread,
}: MessageMenuProps) {
  const t = useTranslate()
  const [open, setOpen] = useState(false)
  const container = useRef<HTMLDivElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: MouseEvent) => {
      if (!container.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false)
        // Return focus where it came from, or the keyboard user is dropped back
        // at the top of the document with no idea what they just closed.
        trigger.current?.focus()
        return
      }
      if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
      const items = Array.from(
        menu.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [],
      )
      if (items.length === 0) return
      event.preventDefault()
      const current = items.indexOf(document.activeElement as HTMLElement)
      const step = event.key === 'ArrowDown' ? 1 : -1
      // Wraps, which is what the ARIA menu pattern specifies.
      const next = (current + step + items.length) % items.length
      items[next]?.focus()
    }
    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  // Move focus into the menu when it opens, so the first arrow key acts on it
  // rather than scrolling the page behind it.
  useEffect(() => {
    if (!open) return
    menu.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
  }, [open])

  // A message still in the outbox has no server id, so nothing here applies.
  if (!message.seq) return null

  const item = (label: string, onClick: () => void, tone?: 'danger') => (
    <button
      key={label}
      type="button"
      // A container with role="menu" whose children are plain buttons is not a
      // menu to a screen reader — it announces a group of unrelated controls and
      // loses the count. The role has to be on the items too.
      role="menuitem"
      onClick={() => {
        setOpen(false)
        onClick()
      }}
      className={cn(
        'hover:bg-surface-hover w-full rounded-lg px-2.5 py-1.5 text-left text-sm transition-colors',
        tone === 'danger' && 'text-danger',
      )}
    >
      {label}
    </button>
  )

  return (
    <div ref={container} className="relative">
      <button
        ref={trigger}
        type="button"
        onClick={() => setOpen((value) => !value)}
        aria-label={t('chat.actions')}
        aria-haspopup="menu"
        aria-expanded={open}
        className="text-ink-faint hover:bg-surface-hover hover:text-ink rounded-full p-1 transition-colors"
      >
        <svg viewBox="0 0 16 16" className="size-4" fill="currentColor">
          <circle cx="8" cy="3" r="1.4" />
          <circle cx="8" cy="8" r="1.4" />
          <circle cx="8" cy="13" r="1.4" />
        </svg>
      </button>

      {open && (
        <div
          ref={menu}
          role="menu"
          aria-label={t('chat.actions')}
          className="border-line bg-surface-raised absolute top-0 right-0 z-20 w-44 rounded-xl border p-1 shadow-lg"
        >
          <div className="flex justify-between gap-0.5 px-1 pb-1">
            {QUICK_REACTIONS.map((emoji) => (
              <button
                key={emoji}
                type="button"
                role="menuitem"
                aria-label={t('chat.reactWith', { emoji })}
                onClick={() => {
                  setOpen(false)
                  onReact(emoji)
                }}
                className="rounded-md p-1 text-base transition-transform hover:scale-125"
              >
                {emoji}
              </button>
            ))}
          </div>
          <div className="border-line border-t pt-1">
            {item(t('chat.reply'), onReply)}
            {item(t('chat.openThread'), onOpenThread)}
            {item(t('chat.forward'), onForward)}
            {item(pinned ? t('chat.unpin') : t('chat.pin'), onTogglePin)}
            {message.text
              ? item(t('chat.copy'), () => void navigator.clipboard.writeText(message.text))
              : null}
            {canEdit && message.text ? item(t('chat.edit'), onEdit) : null}
            {canDelete ? item(t('chat.delete'), onDelete, 'danger') : null}
          </div>
        </div>
      )}
    </div>
  )
}
