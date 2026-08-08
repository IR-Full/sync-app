'use client'

import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'

import type { MessageAttachment } from '@/entities/message'
import { useConnectionState } from '@/shared/api'
import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { Spinner } from '@/shared/ui'

import type { SendOptions } from '../model/use-send-message'

/** Offered self-destruct windows, in seconds. */
const TTL_CHOICES = [0, 10, 60, 3600, 86_400]

export interface MessageComposerProps {
  onSend: (text: string, options?: SendOptions) => void
  onTyping?: (active: boolean) => void
  onDraftChange?: (text: string) => void
  /** uploads a file and resolves to the attachment descriptor */
  onUpload?: (file: File) => Promise<MessageAttachment>
  onOpenPoll?: () => void
  onSchedule?: (text: string, sendAt: number) => void
  disabled?: boolean
  /** restored draft text for this chat */
  initialText?: string
  /** message being replied to */
  replyTo?: { id: string; label: string } | null
  onCancelReply?: () => void
  /** message being edited — submitting saves instead of sending */
  editing?: { id: string; text: string } | null
  onSubmitEdit?: (text: string) => void
  onCancelEdit?: () => void
}

export function MessageComposer({
  onSend,
  onTyping,
  onDraftChange,
  onUpload,
  onOpenPoll,
  onSchedule,
  disabled,
  initialText,
  replyTo,
  onCancelReply,
  editing,
  onSubmitEdit,
  onCancelEdit,
}: MessageComposerProps) {
  const t = useTranslate()
  const state = useConnectionState()
  // Seeded once. Resetting for a different chat or a new edit target is the
  // caller's job, done by changing this component's `key` — which is React's
  // answer to "reset state when a prop changes" and avoids an effect that
  // writes state on every render pass.
  const [text, setText] = useState(initialText ?? '')
  const [attachment, setAttachment] = useState<MessageAttachment | null>(null)
  const [uploading, setUploading] = useState(false)
  const [uploadError, setUploadError] = useState(false)
  const [ttlSeconds, setTtlSeconds] = useState(0)
  const textarea = useRef<HTMLTextAreaElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  // Focus on mount — the composer is remounted whenever the chat or edit target
  // changes, so this covers both cases without watching props.
  useEffect(() => {
    textarea.current?.focus()
  }, [])

  // Grow with the content up to a ceiling, then scroll inside.
  useEffect(() => {
    const element = textarea.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = `${Math.min(element.scrollHeight, 160)}px`
  }, [text])

  function submit() {
    const value = text.trim()
    if (editing) {
      if (value) onSubmitEdit?.(value)
      setText('')
      return
    }
    if (!value && !attachment) return
    onSend(value, {
      attachment,
      replyTo: replyTo?.id,
      ttlSeconds,
    })
    setText('')
    setAttachment(null)
    onTyping?.(false)
    onDraftChange?.('')
    onCancelReply?.()
  }

  function onKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    // Enter sends; Shift+Enter breaks the line.
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      submit()
    }
    if (event.key === 'Escape' && editing) onCancelEdit?.()
  }

  async function pickFile(file: File | undefined) {
    if (!file || !onUpload) return
    setUploading(true)
    setUploadError(false)
    try {
      setAttachment(await onUpload(file))
    } catch {
      setUploadError(true)
    } finally {
      setUploading(false)
      if (fileInput.current) fileInput.current.value = ''
    }
  }

  const ttlLabel = (seconds: number) => {
    if (seconds === 0) return t('chat.ttlOff')
    if (seconds < 60) return t('chat.ttlSeconds', { count: seconds })
    if (seconds < 3600) return t('chat.ttlMinutes', { count: seconds / 60 })
    return t('chat.ttlHours', { count: seconds / 3600 })
  }

  return (
    <div className="border-line bg-surface border-t">
      {(replyTo || editing) && (
        <div className="border-line flex items-center gap-2 border-b px-3 py-1.5 text-xs">
          <span className="text-ink-muted min-w-0 flex-1 truncate">
            {editing ? t('chat.editing') : t('chat.replyingTo', { name: replyTo!.label })}
          </span>
          <button
            type="button"
            onClick={() => {
              if (editing) {
                onCancelEdit?.()
                setText('')
              } else onCancelReply?.()
            }}
            className="text-accent hover:underline"
          >
            {t('chat.cancelEdit')}
          </button>
        </div>
      )}

      {(attachment || uploading || uploadError) && (
        <div className="border-line flex items-center gap-2 border-b px-3 py-1.5 text-xs">
          {uploading ? (
            <>
              <Spinner className="size-3.5" />
              <span className="text-ink-muted">{t('chat.uploading')}</span>
            </>
          ) : uploadError ? (
            <span className="text-danger">{t('chat.uploadFailed')}</span>
          ) : (
            <>
              <span className="text-ink min-w-0 flex-1 truncate">{attachment!.filename}</span>
              <button
                type="button"
                onClick={() => setAttachment(null)}
                className="text-accent hover:underline"
              >
                {t('common.cancel')}
              </button>
            </>
          )}
        </div>
      )}

      <form
        onSubmit={(event: FormEvent) => {
          event.preventDefault()
          submit()
        }}
        className="flex items-end gap-1.5 px-3 py-2.5"
      >
        {onUpload && !editing && (
          <>
            <input
              ref={fileInput}
              type="file"
              hidden
              onChange={(event) => void pickFile(event.target.files?.[0])}
            />
            <button
              type="button"
              onClick={() => fileInput.current?.click()}
              aria-label={t('chat.attach')}
              title={t('chat.attach')}
              className="text-ink-muted hover:bg-surface-hover hover:text-ink flex size-9 shrink-0 items-center justify-center rounded-full transition-colors"
            >
              <svg
                viewBox="0 0 20 20"
                className="size-5"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.6"
              >
                <path
                  d="M13.5 8.5l-4.6 4.6a2.2 2.2 0 003.1 3.1l5-5a3.7 3.7 0 00-5.2-5.2l-5 5a5.2 5.2 0 007.4 7.4l4.3-4.3"
                  strokeLinecap="round"
                  transform="scale(0.8) translate(2 -1)"
                />
              </svg>
            </button>
          </>
        )}

        {onOpenPoll && !editing && (
          <button
            type="button"
            onClick={onOpenPoll}
            aria-label={t('chat.poll')}
            title={t('chat.poll')}
            className="text-ink-muted hover:bg-surface-hover hover:text-ink flex size-9 shrink-0 items-center justify-center rounded-full transition-colors"
          >
            <svg
              viewBox="0 0 20 20"
              className="size-5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <path d="M4 15V9M10 15V5M16 15v-4" strokeLinecap="round" />
            </svg>
          </button>
        )}

        <textarea
          ref={textarea}
          rows={1}
          value={text}
          disabled={disabled}
          onChange={(event) => {
            setText(event.target.value)
            onTyping?.(event.target.value.trim().length > 0)
            onDraftChange?.(event.target.value)
          }}
          onBlur={() => onTyping?.(false)}
          onKeyDown={onKeyDown}
          placeholder={t('chat.placeholder')}
          aria-label={t('chat.placeholder')}
          className={cn(
            'border-line bg-surface-raised text-ink max-h-40 min-h-9 flex-1 resize-none rounded-2xl border px-3.5 py-2 text-sm',
            'placeholder:text-ink-faint focus:outline-accent focus:outline-2',
            'disabled:cursor-not-allowed disabled:opacity-60',
          )}
        />

        {!editing && (
          <select
            aria-label={t('chat.ttl')}
            title={t('chat.ttl')}
            value={ttlSeconds}
            onChange={(event) => setTtlSeconds(Number(event.target.value))}
            className={cn(
              'border-line bg-surface-raised h-9 shrink-0 rounded-full border px-2 text-xs',
              ttlSeconds > 0 && 'border-accent text-accent',
            )}
          >
            {TTL_CHOICES.map((seconds) => (
              <option key={seconds} value={seconds}>
                {ttlLabel(seconds)}
              </option>
            ))}
          </select>
        )}

        {onSchedule && !editing && (
          <button
            type="button"
            aria-label={t('chat.schedule')}
            title={t('chat.schedule')}
            onClick={() => {
              const when = window.prompt(t('chat.scheduleAt'), '+1h')
              if (!when) return
              const relative = /^\+(\d+)([mh])$/.exec(when.trim())
              const sendAt = relative
                ? Date.now() + Number(relative[1]) * (relative[2] === 'h' ? 3_600_000 : 60_000)
                : Date.parse(when)
              if (!Number.isFinite(sendAt)) return
              onSchedule(text.trim(), sendAt)
              setText('')
            }}
            disabled={!text.trim()}
            className="text-ink-muted hover:bg-surface-hover hover:text-ink flex size-9 shrink-0 items-center justify-center rounded-full transition-colors disabled:opacity-40"
          >
            <svg
              viewBox="0 0 20 20"
              className="size-5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <circle cx="10" cy="10" r="7" />
              <path d="M10 6v4.2l2.6 1.6" strokeLinecap="round" />
            </svg>
          </button>
        )}

        <button
          type="submit"
          disabled={disabled || (!text.trim() && !attachment)}
          aria-label={t('chat.send')}
          title={state !== 'ready' ? t('chat.queued') : t('chat.send')}
          className={cn(
            'flex size-9 shrink-0 items-center justify-center rounded-full transition-colors',
            'bg-accent text-accent-ink hover:bg-accent-hover',
            'focus-visible:outline-accent focus-visible:outline-2 focus-visible:outline-offset-2',
            'disabled:cursor-not-allowed disabled:opacity-45',
          )}
        >
          <svg viewBox="0 0 20 20" className="size-5" fill="currentColor">
            <path d="M2.3 17.5 18.6 10.6c.7-.3.7-1.3 0-1.6L2.3 2.1c-.6-.3-1.3.3-1.1 1L2.8 9c.1.3.3.5.6.5l7.4 1c.3 0 .3.5 0 .5l-7.4 1c-.3 0-.5.2-.6.5L1.2 16.6c-.2.7.5 1.2 1.1.9z" />
          </svg>
        </button>
      </form>
    </div>
  )
}
