'use client'

import { useRouter } from 'next/navigation'
import { useSyncExternalStore } from 'react'

import { useSettingsStore } from '@/entities/settings'
import { SecurityPanel } from '@/features/account-security'
import { useLogout } from '@/features/auth'
import { PremiumPanel } from '@/features/billing'
import { PrivacyPanel } from '@/features/privacy'
import { DeleteAccountPanel, SessionsPanel } from '@/features/sessions'
import { usePushToken } from '@/features/push-token'
import { LOCALE_LABELS, LOCALES, useLocaleStore, useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { ACCENTS, useThemeStore, type Accent, type ThemeMode } from '@/shared/theme/model'
import { useSubscriptionStore } from '@/entities/subscription'
import { Button, Toggle } from '@/shared/ui'

/**
 * Notification permission has no change event, so the store is driven manually:
 * the only thing that can change it from here is our own `requestPermission`.
 */
const permissionListeners = new Set<() => void>()

function subscribeToPermission(onChange: () => void): () => void {
  permissionListeners.add(onChange)
  return () => {
    permissionListeners.delete(onChange)
  }
}

function notifyPermissionChanged(): void {
  permissionListeners.forEach((listener) => listener())
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border-line bg-surface rounded-2xl border p-4">
      <h2 className="text-ink mb-2 text-sm font-semibold">{title}</h2>
      {children}
    </section>
  )
}

function ChoiceRow<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: T
  options: { value: T; label: string }[]
  onChange: (next: T) => void
}) {
  return (
    <div className="flex items-center justify-between gap-4 py-2">
      <span className="text-sm font-medium">{label}</span>
      <div className="bg-surface-sunken flex gap-1 rounded-xl p-1">
        {options.map((option) => (
          <button
            key={option.value}
            type="button"
            onClick={() => onChange(option.value)}
            aria-pressed={value === option.value}
            className={cn(
              'rounded-lg px-3 py-1 text-xs font-medium transition-colors',
              value === option.value
                ? 'bg-surface-raised text-ink shadow-sm'
                : 'text-ink-muted hover:text-ink',
            )}
          >
            {option.label}
          </button>
        ))}
      </div>
    </div>
  )
}

/**
 * The accent picker: swatches rather than names, because the thing being chosen
 * is a colour and a row of words makes you click one to find out what it is.
 *
 * `locked` renders the same swatches disabled with an explanation, instead of
 * hiding the row. Hiding a paid feature means nobody discovers it exists; showing
 * it greyed with a reason is the difference between an upsell and a dead end.
 */
function AccentRow({
  label,
  hint,
  value,
  locked,
  onChange,
}: {
  label: string
  hint: string
  value: Accent
  locked: boolean
  onChange: (next: Accent) => void
}) {
  return (
    <div className="py-2">
      <div className="flex items-center justify-between gap-4">
        <span className="text-sm font-medium">{label}</span>
        <div className="flex gap-2">
          {ACCENTS.map((accent) => (
            <button
              key={accent}
              type="button"
              disabled={locked}
              onClick={() => onChange(accent)}
              aria-label={accent}
              aria-pressed={value === accent}
              data-accent-swatch={accent}
              className={cn(
                'size-6 rounded-full border-2 transition-transform',
                // The swatch paints ITS OWN colour, not the active accent, so the
                // row shows what each option would do rather than five copies of
                // the current choice.
                accent === 'default' && 'bg-[#3b6ef5]',
                accent === 'violet' && 'bg-[#7c4dff]',
                accent === 'emerald' && 'bg-[#0f9d6e]',
                accent === 'amber' && 'bg-[#c2700c]',
                accent === 'rose' && 'bg-[#d6336c]',
                value === accent ? 'border-ink scale-110' : 'border-transparent',
                locked ? 'cursor-not-allowed opacity-40' : 'hover:scale-110',
              )}
            />
          ))}
        </div>
      </div>
      {locked && <p className="text-ink-faint mt-1 text-xs">{hint}</p>}
    </div>
  )
}

export function SettingsPanel() {
  const t = useTranslate()
  const router = useRouter()
  const logout = useLogout()

  const mode = useThemeStore((state) => state.mode)
  const setMode = useThemeStore((state) => state.setMode)
  const accent = useThemeStore((state) => state.accent)
  const setAccent = useThemeStore((state) => state.setAccent)
  // Gated on the ENTITLEMENT, never on `plan === 'premium'`: a deployment with no
  // acquirer reports the plan as free while granting everything, so a name-based
  // check would lock the palettes on exactly the install where they are free.
  const canCustomise = useSubscriptionStore((state) => state.entitlements.customThemes)
  const locale = useLocaleStore((state) => state.locale)
  const setLocale = useLocaleStore((state) => state.setLocale)
  const settings = useSettingsStore()

  // Read through an external store: the permission lives in the browser, not in
  // React, and it must not be sampled during SSR where `Notification` is absent.
  const permission = useSyncExternalStore(
    subscribeToPermission,
    () => (typeof Notification === 'undefined' ? 'unsupported' : Notification.permission),
    () => 'default' as const,
  )

  const push = usePushToken()

  /**
   * Web Push is a separate thing from the in-page notifications above: it is
   * what would let the server reach this browser while the tab is closed, and it
   * only works if the deployment has a VAPID key and a provider behind it.
   */
  const pushStatusLabel =
    push.status === 'registered'
      ? t('settings.push.registered')
      : push.status === 'unsupported'
        ? t('settings.push.unsupported')
        : push.status === 'not-configured'
          ? t('settings.push.notConfigured')
          : push.status === 'denied'
            ? t('settings.notifications.blocked')
            : push.status === 'failed'
              ? t('settings.push.failed')
              : ''

  async function enableNotifications() {
    if (typeof Notification === 'undefined') return
    const result = await Notification.requestPermission()
    notifyPermissionChanged()
    settings.set('desktopNotifications', result === 'granted')
  }

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 p-4">
      {/*
        A sticky header, because this page is long: the panels below it run to
        account deletion, and the only way back used to be a button buried in the
        third section — directly beside Log out, which is a bad thing to reach for
        by mistake. Back belongs where it is always reachable and where nothing
        destructive sits next to it.
      */}
      <header className="bg-surface-sunken/95 sticky top-0 z-10 -mx-4 flex items-center gap-3 px-4 py-3 backdrop-blur">
        <Button variant="secondary" size="small" onClick={() => router.push('/chats')}>
          ← {t('nav.back')}
        </Button>
        <h1 className="text-ink text-base font-semibold">{t('settings.title')}</h1>
      </header>

      <Section title={t('settings.appearance')}>
        <ChoiceRow<ThemeMode>
          label={t('settings.theme')}
          value={mode}
          onChange={setMode}
          options={[
            { value: 'light', label: t('settings.theme.light') },
            { value: 'dark', label: t('settings.theme.dark') },
            { value: 'system', label: t('settings.theme.system') },
          ]}
        />
        <AccentRow
          label={t('settings.accent')}
          hint={t('settings.accent.premium')}
          value={accent}
          locked={!canCustomise}
          onChange={setAccent}
        />
        <ChoiceRow
          label={t('settings.language')}
          value={locale}
          onChange={setLocale}
          options={LOCALES.map((value) => ({ value, label: LOCALE_LABELS[value] }))}
        />
      </Section>

      <Section title={t('settings.notifications')}>
        <Toggle
          label={t('settings.notifications.desktop')}
          description={
            permission === 'denied' ? t('settings.notifications.blocked') : undefined
          }
          checked={settings.desktopNotifications && permission === 'granted'}
          disabled={permission === 'denied' || permission === 'unsupported'}
          onChange={(next) => {
            if (next && permission !== 'granted') {
              void enableNotifications()
              return
            }
            settings.set('desktopNotifications', next)
          }}
        />
        <Toggle
          label={t('settings.notifications.sound')}
          checked={settings.soundOnMessage}
          onChange={(next) => settings.set('soundOnMessage', next)}
        />
        <p className="text-ink-faint mt-1 text-xs">{t('settings.notifications.hint')}</p>

        <div className="border-line mt-3 flex items-center justify-between gap-3 border-t pt-3">
          <span className="min-w-0 text-sm">
            <span className="block font-medium">{t('settings.push')}</span>
            <span className="text-ink-muted text-xs">{pushStatusLabel}</span>
          </span>
          <Button
            size="small"
            variant="secondary"
            disabled={!push.ready || push.status === 'registered'}
            onClick={() => void push.register()}
          >
            {t('settings.push.register')}
          </Button>
        </div>
      </Section>

      <Section title={t('settings.session')}>
        <Toggle
          label={t('settings.readReceipts')}
          checked={settings.sendReadReceipts}
          onChange={(next) => settings.set('sendReadReceipts', next)}
        />
        <div className="mt-3 flex gap-2">
          <Button variant="danger" onClick={logout}>
            {t('nav.logout')}
          </Button>
        </div>
      </Section>

      {/*
        Sessions sit BELOW the ordinary Log out button on purpose. That button
        only forgets the token on this device — which is what most people mean
        by logging out — while this list is where someone goes after losing a
        device and needs the session to actually stop working on the server.
      */}
      {/*
        Security sits above the session list and below the ordinary settings. Above the
        session list because changing the password is what REVOKES those sessions, so
        reading downwards matches the order somebody secures an account in.
      */}
      <SecurityPanel />
      <PremiumPanel />
      <PrivacyPanel />
      <SessionsPanel />
      <DeleteAccountPanel />
    </div>
  )
}
