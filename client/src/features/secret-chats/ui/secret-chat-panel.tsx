'use client'

import { useState } from 'react'

import { useSecretTranscript } from '@/entities/secret-chat'
import { ProtocolError } from '@/shared/api'
import { useLocale, useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { formatTime } from '@/shared/lib/format'
import { Button, EmptyState, ErrorNote, Modal, TextField } from '@/shared/ui'

import { useSecretChat } from '../model/use-secret-chats'

/**
 * An end-to-end encrypted side channel with one peer.
 *
 * Deliberately separate from the ordinary chat, because it is a different thing:
 * these messages never reach the server in readable form, are not stored
 * anywhere, and exist only on the devices that received them. Presenting them in
 * the same transcript would imply a history and a sync that do not exist.
 */
export function SecretChatPanel({
  peerUserId,
  peerLabel,
  open,
  onClose,
}: {
  peerUserId: string
  peerLabel: string
  open: boolean
  onClose: () => void
}) {
  const t = useTranslate()
  const locale = useLocale()
  const messages = useSecretTranscript(peerUserId)
  const { send } = useSecretChat(peerUserId)

  const [text, setText] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit() {
    const value = text.trim()
    if (!value) return
    setPending(true)
    setError(null)
    try {
      await send(value)
      setText('')
    } catch (caught) {
      setError(
        caught instanceof Error && caught.message === 'no-devices'
          ? t('secret.noDevices')
          : caught instanceof ProtocolError
            ? caught.message
            : t('error.unknown'),
      )
    } finally {
      setPending(false)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={t('secret.title', { name: peerLabel })}>
      <p className="text-ink-faint mb-3 text-xs">{t('secret.explainer')}</p>

      <div className="border-line mb-3 max-h-80 min-h-32 overflow-y-auto rounded-xl border p-2">
        {messages.length === 0 ? (
          <EmptyState title={t('secret.empty')} />
        ) : (
          <ul className="flex flex-col gap-1.5">
            {messages.map((message) => (
              <li
                key={message.id}
                className={cn('flex', message.outgoing ? 'justify-end' : 'justify-start')}
              >
                <span
                  className={cn(
                    'max-w-[80%] rounded-xl px-2.5 py-1.5 text-sm',
                    message.failed
                      ? 'bg-danger/10 text-danger italic'
                      : message.outgoing
                        ? 'bg-bubble-own text-bubble-own-ink'
                        : 'bg-bubble-peer text-bubble-peer-ink',
                  )}
                >
                  {message.failed ? t('secret.undecryptable') : message.text}
                  <span className="ml-2 text-[10px] opacity-70">
                    {formatTime(message.timestamp, locale)}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {error && <ErrorNote className="mb-2">{error}</ErrorNote>}

      <div className="flex items-end gap-2">
        <TextField
          placeholder={t('chat.placeholder')}
          value={text}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit()
          }}
          className="flex-1"
        />
        <Button onClick={submit} loading={pending} disabled={!text.trim()}>
          {t('chat.send')}
        </Button>
      </div>
    </Modal>
  )
}
