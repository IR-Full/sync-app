'use client'

import { useState } from 'react'

import { useLocale, useTranslate, type TranslationKey } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { Button, ErrorNote, Modal, Spinner, TextField } from '@/shared/ui'

import {
  useDeleteAccount,
  useRevokeSession,
  useSessions,
  type ActiveSession,
} from '../model/use-sessions'

/**
 * The platform string is whatever the device announced in HELLO, so it is a
 * label rather than an enum — a device row that has since been removed leaves it
 * empty. Mapping known values and falling back keeps an unfamiliar client from
 * rendering as a blank line with a Sign out button next to it.
 */
function platformKey(platform: string): TranslationKey {
  switch (platform) {
    case 'web':
      return 'sessions.platform.web'
    case 'android':
      return 'sessions.platform.android'
    case 'ios':
      return 'sessions.platform.ios'
    case 'cli':
      return 'sessions.platform.cli'
    default:
      return 'sessions.platform.unknown'
  }
}

function SessionRow({
  session,
  onRevoke,
  busy,
}: {
  session: ActiveSession
  onRevoke: (session: ActiveSession) => void
  busy: boolean
}) {
  const t = useTranslate()
  const locale = useLocale()
  const date = (ms: number) =>
    ms > 0 ? new Date(ms).toLocaleDateString(locale, { dateStyle: 'medium' }) : '—'

  return (
    <li
      className={cn(
        'border-line flex items-center gap-3 border-b py-3 last:border-b-0',
        session.current && 'font-medium',
      )}
    >
      <div className="min-w-0 flex-1">
        <p className="text-ink truncate text-sm">
          {t(platformKey(session.platform))}
          {session.current && (
            <span className="text-accent ml-2">· {t('sessions.current')}</span>
          )}
        </p>
        <p className="text-ink-faint text-xs">
          {t('sessions.signedIn', { date: date(session.createdAt) })}
          {' · '}
          {t('sessions.expires', { date: date(session.expiresAt) })}
        </p>
      </div>
      <Button variant="ghost" onClick={() => onRevoke(session)} disabled={busy}>
        {t('sessions.revoke')}
      </Button>
    </li>
  )
}

/**
 * Active sessions, plus the two destructive actions that belong beside them.
 *
 * Revoking the CURRENT session is allowed rather than hidden: it is the honest
 * way to express "log out of this browser and actually end the session", which
 * is what most people believe the ordinary Log out button already does. It is
 * confirmed first, because the button sits in a list where every other row
 * signs out a different device.
 */
export function SessionsPanel() {
  const t = useTranslate()
  const { data: sessions, isPending, isError } = useSessions()
  const revoke = useRevokeSession()
  const [confirmingCurrent, setConfirmingCurrent] = useState<ActiveSession | null>(null)

  const onRevoke = (session: ActiveSession) => {
    if (session.current) {
      setConfirmingCurrent(session)
      return
    }
    revoke.mutate(session.sessionId)
  }

  const others = sessions?.filter((s) => !s.current).length ?? 0

  return (
    <section className="border-line bg-surface rounded-2xl border p-4">
      <h2 className="text-ink mb-1 text-sm font-semibold">{t('sessions.title')}</h2>
      <p className="text-ink-faint mb-3 text-xs">{t('sessions.hint')}</p>

      {isPending && (
        <p className="text-ink-faint flex items-center gap-2 text-sm">
          <Spinner className="size-4" /> {t('sessions.loading')}
        </p>
      )}
      {isError && <ErrorNote>{t('sessions.failed')}</ErrorNote>}

      {sessions && (
        <>
          <ul className="mb-3">
            {sessions.map((session) => (
              <SessionRow
                key={session.sessionId}
                session={session}
                onRevoke={onRevoke}
                busy={revoke.isPending}
              />
            ))}
          </ul>

          <Button
            variant="secondary"
            onClick={() => revoke.mutate(null)}
            disabled={revoke.isPending || others === 0}
          >
            {t('sessions.revokeOthers')}
          </Button>
          <p className="text-ink-faint mt-1 text-xs">
            {others === 0 ? t('sessions.revokedNone') : t('sessions.revokeOthersHint')}
          </p>
        </>
      )}

      {revoke.isError && <ErrorNote>{t('sessions.revokeFailed')}</ErrorNote>}

      <Modal
        open={confirmingCurrent !== null}
        onClose={() => setConfirmingCurrent(null)}
        title={t('sessions.title')}
      >
        <div className="p-4">
          <p className="text-ink mb-4 text-sm">{t('sessions.confirmCurrent')}</p>
          <div className="flex gap-2">
            <Button
              variant="danger"
              onClick={() => {
                const target = confirmingCurrent
                setConfirmingCurrent(null)
                if (target) revoke.mutate(target.sessionId)
              }}
            >
              {t('sessions.revoke')}
            </Button>
            <Button variant="ghost" onClick={() => setConfirmingCurrent(null)}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      </Modal>
    </section>
  )
}

/**
 * Account deletion, kept in its own block away from the session list.
 *
 * The password field is not ceremony: the server re-checks it because a session
 * token lives on the device, so this is the step that stops someone holding an
 * unlocked phone from erasing the account behind it.
 */
export function DeleteAccountPanel() {
  const t = useTranslate()
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const deleteAccount = useDeleteAccount()

  const submit = () => {
    if (!password) return
    deleteAccount.mutate({ password })
  }

  return (
    <section className="border-danger/40 bg-surface rounded-2xl border p-4">
      <h2 className="text-danger mb-1 text-sm font-semibold">{t('account.danger')}</h2>
      <p className="text-ink-faint mb-3 text-xs">{t('account.dangerHint')}</p>
      <Button variant="danger" onClick={() => setOpen(true)}>
        {t('account.delete')}
      </Button>

      <Modal open={open} onClose={() => setOpen(false)} title={t('account.delete')}>
        <div className="p-4">
          <p className="text-ink-faint mb-3 text-xs">{t('account.dangerHint')}</p>
          <TextField
            type="password"
            label={t('account.confirmPassword')}
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            autoComplete="current-password"
          />
          {deleteAccount.isError && <ErrorNote>{t('account.deleteFailed')}</ErrorNote>}
          <div className="mt-4 flex gap-2">
            <Button
              variant="danger"
              onClick={submit}
              disabled={!password || deleteAccount.isPending}
            >
              {deleteAccount.isPending ? t('account.deleting') : t('account.delete')}
            </Button>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      </Modal>
    </section>
  )
}
