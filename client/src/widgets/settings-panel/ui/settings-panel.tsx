'use client'

import { useRouter } from 'next/navigation'
import { useSyncExternalStore } from 'react'

import { useSettingsStore } from '@/entities/settings'
import { useLogout } from '@/features/auth'
import { LOCALE_LABELS, LOCALES, useLocaleStore, useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { useThemeStore, type ThemeMode } from '@/shared/theme/model'
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

export function SettingsPanel() {
  const t = useTranslate()
  const router = useRouter()
  const logout = useLogout()

  const mode = useThemeStore((state) => state.mode)
  const setMode = useThemeStore((state) => state.setMode)
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

  async function enableNotifications() {
    if (typeof Notification === 'undefined') return
    const result = await Notification.requestPermission()
    notifyPermissionChanged()
    settings.set('desktopNotifications', result === 'granted')
  }

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 p-4">
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
      </Section>

      <Section title={t('settings.session')}>
        <Toggle
          label={t('settings.readReceipts')}
          checked={settings.sendReadReceipts}
          onChange={(next) => settings.set('sendReadReceipts', next)}
        />
        <div className="mt-3 flex gap-2">
          <Button variant="secondary" onClick={() => router.push('/chats')}>
            {t('nav.back')}
          </Button>
          <Button variant="danger" onClick={logout}>
            {t('nav.logout')}
          </Button>
        </div>
      </Section>
    </div>
  )
}
