'use client'

import { useEffect, useId, useRef, useState } from 'react'

import { isArchived, isMuted, isPinned, type ChatSummary } from '@/entities/chat'
import { useTranslate, type TranslationKey } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'

import { useChatFlags } from '../model/use-chat-flags'

/**
 * Mute durations, in milliseconds.
 *
 * Durations rather than a switch, because the server stores a DEADLINE — and the
 * reason it does is that "mute for eight hours" is what muting almost always means.
 * The boolean this replaced could only express "forever", which is why nothing ever
 * set it.
 */
const MUTE_PRESETS: { key: TranslationKey; ms: number }[] = [
  { key: 'chats.mute.hour', ms: 60 * 60 * 1000 },
  { key: 'chats.mute.eightHours', ms: 8 * 60 * 60 * 1000 },
  { key: 'chats.mute.week', ms: 7 * 24 * 60 * 60 * 1000 },
  // Ten years. Long enough to read as "forever" and short enough to stay inside the
  // bound the server clamps a deadline to.
  { key: 'chats.mute.forever', ms: 10 * 365 * 24 * 60 * 60 * 1000 },
]

/** One row of the popover. */
function Item({
  children,
  onSelect,
}: {
  children: React.ReactNode
  onSelect: () => void
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onSelect}
      className="text-ink hover:bg-surface-hover w-full rounded-lg px-3 py-1.5 text-left text-xs transition-colors"
    >
      {children}
    </button>
  )
}

export interface ChatFlagsMenuProps {
  chat: ChatSummary
}

/**
 * The per-chat actions: pin, mute for a while, archive.
 *
 * A hand-rolled popover rather than a dependency. The project has no UI library by
 * policy, and what this needs is small: a button, a list, dismissal on outside click
 * or Escape, and focus that does not escape into the link underneath.
 */
export function ChatFlagsMenu({ chat }: ChatFlagsMenuProps) {
  const t = useTranslate()
  const flags = useChatFlags()
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const menuId = useId()

  useEffect(() => {
    if (!open) return

    function onPointerDown(event: PointerEvent) {
      if (!containerRef.current?.contains(event.target as Node)) setOpen(false)
    }
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') setOpen(false)
    }

    // `pointerdown`, not `click`: a click listener fires after the menu item's own
    // handler has already run and re-rendered, at which point the target is detached
    // and `contains` is false — so the menu closed on every selection whether the
    // click was inside or out.
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  function run(action: () => Promise<void>) {
    setOpen(false)
    // Deliberately not awaited and deliberately not surfaced. These three writes are
    // idempotent, and their whole visible effect is what the row looks like next —
    // which the store updates from the server's echo. A failed mute leaves the row
    // unmuted, which is the honest outcome and needs no dialog.
    void action().catch(() => {})
  }

  const muted = isMuted(chat)
  const pinned = isPinned(chat)
  const archived = isArchived(chat)

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        aria-label={t('chats.actions')}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((previous) => !previous)}
        className={cn(
          'text-ink-faint hover:text-ink hover:bg-surface-raised rounded-lg p-1.5 transition-colors',
          open && 'bg-surface-raised text-ink',
        )}
      >
        <svg viewBox="0 0 16 16" className="size-4" fill="currentColor">
          <circle cx="8" cy="3" r="1.4" />
          <circle cx="8" cy="8" r="1.4" />
          <circle cx="8" cy="13" r="1.4" />
        </svg>
      </button>

      {open && (
        <div
          id={menuId}
          role="menu"
          className="border-line bg-surface-raised absolute right-0 z-20 mt-1 w-48 rounded-xl border p-1 shadow-lg"
        >
          <Item onSelect={() => run(() => flags.setPinned(chat.id, !pinned))}>
            {pinned ? t('chats.unpin') : t('chats.pin')}
          </Item>

          {muted ? (
            <Item onSelect={() => run(() => flags.unmute(chat.id))}>{t('chats.unmute')}</Item>
          ) : (
            <>
              <p className="text-ink-faint px-3 pt-1.5 pb-0.5 text-[10px] font-semibold tracking-wide uppercase">
                {t('chats.mute')}
              </p>
              {MUTE_PRESETS.map((preset) => (
                <Item
                  key={preset.key}
                  onSelect={() => run(() => flags.muteFor(chat.id, preset.ms))}
                >
                  {t(preset.key)}
                </Item>
              ))}
            </>
          )}

          <div className="border-line my-1 border-t" />

          <Item
            onSelect={() =>
              run(async () => {
                // Archiving a pinned chat unpins it: leaving it pinned puts it at the
                // top of a list the user has just said they do not want to look at.
                if (!archived && pinned) await flags.setPinned(chat.id, false)
                await flags.setArchived(chat.id, !archived)
              })
            }
          >
            {archived ? t('chats.unarchive') : t('chats.archive')}
          </Item>
        </div>
      )}
    </div>
  )
}
