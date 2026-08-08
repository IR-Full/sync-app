'use client'

import { useRouter } from 'next/navigation'
import { useState } from 'react'

import { useSessionStore } from '@/entities/session'
import { useTranslate } from '@/shared/i18n'
import { Avatar, Button, TextField } from '@/shared/ui'

function ReadOnlyRow({ label, value }: { label: string; value: string }) {
  const t = useTranslate()
  const [copied, setCopied] = useState(false)

  return (
    <div className="border-line flex items-center justify-between gap-3 border-b py-2.5 last:border-0">
      <span className="text-ink-muted text-sm">{label}</span>
      <span className="flex min-w-0 items-center gap-2">
        <code className="text-ink truncate font-mono text-xs">{value || '—'}</code>
        {value && (
          <button
            type="button"
            onClick={async () => {
              await navigator.clipboard.writeText(value)
              setCopied(true)
              setTimeout(() => setCopied(false), 1500)
            }}
            className="text-accent shrink-0 text-xs underline-offset-4 hover:underline"
          >
            {copied ? t('common.copied') : t('common.copy')}
          </button>
        )}
      </span>
    </div>
  )
}

/**
 * Profile view.
 *
 * Read-only where the server is read-only. The protocol exposes no message for
 * changing a display name or uploading an avatar — the user model carries a
 * `display_name` the gateway never lets a client write, and has no avatar field
 * at all. Rather than fake a save that goes nowhere, the display name is stored
 * locally and labelled as such.
 */
export function ProfilePanel() {
  const t = useTranslate()
  const router = useRouter()
  const session = useSessionStore((state) => state.session)
  const updateSession = useSessionStore((state) => state.updateSession)

  const [displayName, setDisplayName] = useState(session?.displayName ?? '')
  const [saved, setSaved] = useState(false)

  if (!session) return null

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 p-4">
      <section className="border-line bg-surface flex flex-col items-center gap-3 rounded-2xl border p-6">
        <Avatar
          seed={session.userId}
          name={session.displayName || session.username}
          size="large"
        />
        <p className="text-ink text-lg font-semibold">@{session.username}</p>
        <p className="text-ink-faint max-w-sm text-center text-xs">{t('profile.avatarHint')}</p>
      </section>

      <section className="border-line bg-surface rounded-2xl border p-4">
        <div className="flex flex-col gap-3">
          <TextField
            label={t('profile.displayName')}
            hint={t('profile.displayNameHint')}
            value={displayName}
            maxLength={64}
            onChange={(event) => {
              setDisplayName(event.target.value)
              setSaved(false)
            }}
          />
          <div className="flex items-center gap-3">
            <Button
              onClick={() => {
                updateSession({ displayName: displayName.trim() })
                setSaved(true)
              }}
            >
              {t('profile.save')}
            </Button>
            {saved && <span className="text-success text-sm">{t('profile.saved')}</span>}
          </div>
        </div>
      </section>

      <section className="border-line bg-surface rounded-2xl border p-4">
        <h2 className="text-ink mb-1 text-sm font-semibold">{t('settings.session')}</h2>
        <ReadOnlyRow label={t('profile.userId')} value={session.userId} />
        <ReadOnlyRow label={t('profile.deviceId')} value={session.deviceId} />
        <ReadOnlyRow label={t('profile.sessionId')} value={session.sessionId} />
      </section>

      <Button variant="secondary" onClick={() => router.push('/chats')}>
        {t('nav.back')}
      </Button>
    </div>
  )
}
