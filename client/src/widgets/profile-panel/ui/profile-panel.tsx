'use client'

import { useRouter } from 'next/navigation'
import { useRef, useState } from 'react'

import { useSessionStore } from '@/entities/session'
import { useMediaUpload, useMediaUrl } from '@/features/media'
import { useUpdateProfile } from '@/features/profile'
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
 * The name and avatar live on the SERVER: PROFILE_SET writes them, the gateway
 * mirrors the change to this account's other devices, and AUTH_OK returns them
 * on every connect. The session store holds a copy so the first frame renders
 * without a round trip — it is a cache of server state, not the source.
 *
 * An avatar is a media_ref, so it goes through the ordinary pipeline
 * (MEDIA_INIT → signed PUT → ref) and is displayed through a signed download
 * URL. That is why the upload finishes before the profile is written: a ref
 * that failed to upload must never be saved.
 */
export function ProfilePanel() {
  const t = useTranslate()
  const router = useRouter()
  const session = useSessionStore((state) => state.session)
  const updateProfile = useUpdateProfile()
  const { upload, progress } = useMediaUpload()
  const fileInput = useRef<HTMLInputElement>(null)

  // The field follows the server until the user types into it. `null` means
  // "not edited", which is what lets the session hydrate late (it is restored in
  // an effect, so the first render has no session at all) and lets a change made
  // on another device land in an untouched field instead of being overwritten by
  // a stale initial value.
  const [draftName, setDraftName] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const avatarRef = session?.avatarRef ?? ''
  const { data: avatarUrl } = useMediaUrl(avatarRef)

  if (!session) return null

  const busy = updateProfile.isPending || progress !== null
  const displayName = draftName ?? session.displayName ?? ''

  async function save(update: Parameters<typeof updateProfile.mutateAsync>[0]) {
    setError(null)
    setSaved(false)
    try {
      await updateProfile.mutateAsync(update)
      // Hand the field back to the server's copy now that they agree.
      setDraftName(null)
      setSaved(true)
    } catch {
      setError(t('profile.saveFailed'))
    }
  }

  async function pickAvatar(file: File) {
    setError(null)
    setSaved(false)
    try {
      const { mediaRef } = await upload(file)
      await save({ avatarRef: mediaRef })
    } catch {
      setError(t('profile.avatarFailed'))
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 p-4">
      <section className="border-line bg-surface flex flex-col items-center gap-3 rounded-2xl border p-6">
        <Avatar
          seed={session.userId}
          name={session.displayName || session.username}
          src={avatarUrl}
          size="large"
          preload
        />
        <p className="text-ink text-lg font-semibold">@{session.username}</p>

        <input
          ref={fileInput}
          type="file"
          accept="image/*"
          className="hidden"
          onChange={(event) => {
            const file = event.target.files?.[0]
            // Reset first: picking the same file twice must fire onChange again.
            event.target.value = ''
            if (file) void pickAvatar(file)
          }}
        />
        <div className="flex items-center gap-3">
          <Button
            variant="secondary"
            disabled={busy}
            onClick={() => fileInput.current?.click()}
          >
            {progress !== null ? t('profile.avatarUploading') : t('profile.avatarChange')}
          </Button>
          {avatarRef && (
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => void save({ clearAvatar: true })}
            >
              {t('profile.avatarRemove')}
            </Button>
          )}
        </div>
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
              setDraftName(event.target.value)
              setSaved(false)
            }}
          />
          <div className="flex items-center gap-3">
            <Button
              disabled={busy || displayName.trim().length === 0}
              onClick={() => void save({ displayName: displayName.trim() })}
            >
              {t('profile.save')}
            </Button>
            {saved && <span className="text-success text-sm">{t('profile.saved')}</span>}
            {error && <span className="text-danger text-sm">{error}</span>}
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
