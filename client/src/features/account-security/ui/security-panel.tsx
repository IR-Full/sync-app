'use client'

import { useEffect, useState } from 'react'

import { ErrorCode, ProtocolError } from '@/shared/api'
import { useTranslate } from '@/shared/i18n'
import { Button, TextField } from '@/shared/ui'

import { useAccountSecurity } from '../model/use-account-security'

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border-line bg-surface rounded-2xl border p-4">
      <h2 className="text-ink mb-2 text-sm font-semibold">{title}</h2>
      {children}
    </section>
  )
}

/**
 * Translates a protocol failure into something the user can act on.
 *
 * The three codes worth separating all arrive on the same screen and mean different
 * things: a wrong second-factor code is worth retrying, a demanded one means a field
 * has not been filled in, and `FORBIDDEN` here means the CURRENT password was wrong —
 * not the new one. Collapsing them into "something went wrong" makes a recoverable
 * mistake look like a broken feature.
 */
function describe(error: unknown, t: ReturnType<typeof useTranslate>): string {
  if (error instanceof ProtocolError) {
    if (error.code === ErrorCode.TWO_FACTOR_INVALID) return t('security.code.invalid')
    if (error.code === ErrorCode.TWO_FACTOR_REQUIRED) return t('security.code.required')
    if (error.message) return error.message
  }
  return t('error.unknown')
}

/** The recovery codes, shown once and then gone. */
function RecoveryCodes({ codes }: { codes: string[] }) {
  const t = useTranslate()
  return (
    <div className="border-line bg-surface-sunken mt-3 rounded-xl border p-3">
      <p className="text-ink text-xs font-semibold">{t('security.twoFactor.recovery')}</p>
      <ul className="text-ink mt-2 grid grid-cols-2 gap-1 font-mono text-xs">
        {codes.map((code) => (
          <li key={code} className="select-all">
            {code}
          </li>
        ))}
      </ul>
      {/*
        The warning sits BELOW the codes rather than above. Above, it is read before
        there is anything to apply it to; below, it is the last thing seen before the
        only chance to copy them is gone — the server keeps argon2id hashes, so there
        is no second showing.
      */}
      <p className="text-ink-muted mt-2 text-xs">{t('security.twoFactor.recoveryHint')}</p>
    </div>
  )
}

/**
 * Password and two-factor settings.
 *
 * Neither existed. The password could not be changed by any path — so a leaked one
 * made the account permanently compromised rather than temporarily — and there was no
 * second factor at all, which is the other half of the same gap: one interceptable
 * credential with nothing behind it.
 */
export function SecurityPanel() {
  const t = useTranslate()
  const security = useAccountSecurity()

  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [passwordNotice, setPasswordNotice] = useState('')
  const [passwordError, setPasswordError] = useState('')

  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null)
  const [code, setCode] = useState('')
  const [disablePassword, setDisablePassword] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])
  const [twoFactorError, setTwoFactorError] = useState('')
  const [busy, setBusy] = useState(false)

  // Read on mount. TOTP_STATE is answerable as a question now; while it was
  // reply-only this panel had no way to know the truth and drew "off" on every fresh
  // page load — which invites a second enrolment and reports the refusal as an error.
  useEffect(() => {
    void security.refresh().catch(() => {})
    // Deliberately once: `refresh` is stable and re-running it on every render would
    // make a request per keystroke in the fields below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function changePassword() {
    setPasswordError('')
    setPasswordNotice('')
    if (next !== confirm) {
      setPasswordError(t('security.password.mismatch'))
      return
    }
    setBusy(true)
    try {
      const revoked = await security.changePassword(current, next)
      setCurrent('')
      setNext('')
      setConfirm('')
      setPasswordNotice(t('security.password.changed', { count: revoked }))
    } catch (error) {
      setPasswordError(describe(error, t))
    } finally {
      setBusy(false)
    }
  }

  async function runTwoFactor(work: () => Promise<void>) {
    setTwoFactorError('')
    setBusy(true)
    try {
      await work()
    } catch (error) {
      setTwoFactorError(describe(error, t))
    } finally {
      setBusy(false)
    }
  }

  const enabled = security.twoFactor?.enabled === true
  const available = security.twoFactor !== null

  return (
    <>
      <Section title={t('security.password')}>
        <div className="flex flex-col gap-2">
          <TextField
            type="password"
            label={t('security.password.current')}
            value={current}
            onChange={(event) => setCurrent(event.target.value)}
            autoComplete="current-password"
          />
          <TextField
            type="password"
            label={t('security.password.new')}
            value={next}
            onChange={(event) => setNext(event.target.value)}
            autoComplete="new-password"
          />
          <TextField
            type="password"
            label={t('security.password.confirm')}
            value={confirm}
            onChange={(event) => setConfirm(event.target.value)}
            autoComplete="new-password"
          />
          {passwordError && <p className="text-danger text-xs">{passwordError}</p>}
          {passwordNotice && (
            <p className="text-xs text-emerald-600 dark:text-emerald-400">{passwordNotice}</p>
          )}
          <div>
            <Button
              size="small"
              disabled={busy || !current || !next || !confirm}
              onClick={() => void changePassword()}
            >
              {t('security.password')}
            </Button>
          </div>
        </div>
      </Section>

      <Section title={t('security.twoFactor')}>
        <p className="text-ink-muted mb-3 text-xs">
          {!available
            ? t('security.twoFactor.unknown')
            : enabled
              ? `${t('security.twoFactor.on')} · ${t('security.twoFactor.recoveryLeft', {
                  count: security.twoFactor?.recoveryLeft ?? 0,
                })}`
            : t('security.twoFactor.off')}
        </p>

        {available && enabled && (
          <div className="flex flex-col gap-2">
            {/* Both are required to turn it off: a stolen session must not be able to
                remove the factor that keeps its holder out of the next login. */}
            <TextField
              type="password"
              label={t('security.password.current')}
              value={disablePassword}
              onChange={(event) => setDisablePassword(event.target.value)}
              autoComplete="current-password"
            />
            <TextField
              label={t('security.twoFactor.code')}
              value={code}
              onChange={(event) => setCode(event.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
            />
            <div>
              <Button
                size="small"
                variant="danger"
                disabled={busy || !disablePassword || !code}
                onClick={() =>
                  void runTwoFactor(async () => {
                    await security.disableTOTP(disablePassword, code)
                    setDisablePassword('')
                    setCode('')
                    setRecoveryCodes([])
                  })
                }
              >
                {t('security.twoFactor.disable')}
              </Button>
            </div>
          </div>
        )}

        {available && !enabled && setup && (
          <div className="flex flex-col gap-2">
            {/* The key as TEXT, not only as a QR code: an authenticator on this same
                machine has no camera to point at the screen, and typing the key is
                then the only thing that works. */}
            <div className="border-line bg-surface-sunken rounded-xl border p-3">
              <p className="text-ink-faint text-[10px] font-semibold tracking-wide uppercase">
                {t('security.twoFactor.secret')}
              </p>
              <p className="text-ink mt-1 font-mono text-xs break-all select-all">
                {setup.secret}
              </p>
            </div>
            <p className="text-ink-muted text-xs">{t('security.twoFactor.scan')}</p>
            <TextField
              label={t('security.twoFactor.code')}
              value={code}
              onChange={(event) => setCode(event.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
            />
            <div>
              <Button
                size="small"
                disabled={busy || !code}
                onClick={() =>
                  void runTwoFactor(async () => {
                    const codes = await security.confirmTOTP(code)
                    setRecoveryCodes(codes)
                    setSetup(null)
                    setCode('')
                  })
                }
              >
                {t('common.confirm')}
              </Button>
            </div>
          </div>
        )}

        {available && !enabled && !setup && (
          <Button
            size="small"
            variant="secondary"
            disabled={busy}
            onClick={() =>
              void runTwoFactor(async () => {
                setSetup(await security.beginTOTP())
              })
            }
          >
            {t('security.twoFactor.enable')}
          </Button>
        )}

        {twoFactorError && <p className="text-danger mt-2 text-xs">{twoFactorError}</p>}
        {recoveryCodes.length > 0 && <RecoveryCodes codes={recoveryCodes} />}
      </Section>
    </>
  )
}
