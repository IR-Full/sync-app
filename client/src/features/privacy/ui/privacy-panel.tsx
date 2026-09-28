'use client'

import { useTranslate, type TranslationKey } from '@/shared/i18n'
import { ErrorNote, Spinner } from '@/shared/ui'

import {
  usePrivacy,
  useSetPrivacy,
  VISIBILITIES,
  type PrivacySettings,
  type Visibility,
} from '../model/use-privacy'

function visibilityLabel(v: Visibility): TranslationKey {
  switch (v) {
    case 'contacts':
      return 'privacy.contacts'
    case 'nobody':
      return 'privacy.nobody'
    default:
      return 'privacy.everyone'
  }
}

function Row({
  label,
  hint,
  value,
  disabled,
  onChange,
}: {
  label: string
  hint: string
  value: Visibility
  disabled: boolean
  onChange: (next: Visibility) => void
}) {
  const t = useTranslate()
  return (
    <div className="border-line border-b py-3 last:border-b-0">
      <div className="flex items-center justify-between gap-3">
        <span className="text-ink text-sm">{label}</span>
        <div className="flex gap-1">
          {VISIBILITIES.map((option) => (
            <button
              key={option}
              type="button"
              disabled={disabled}
              onClick={() => onChange(option)}
              aria-pressed={option === value}
              className={
                option === value
                  ? 'bg-accent rounded-lg px-2.5 py-1 text-xs text-white'
                  : 'bg-surface-hover text-ink-muted rounded-lg px-2.5 py-1 text-xs'
              }
            >
              {t(visibilityLabel(option))}
            </button>
          ))}
        </div>
      </div>
      <p className="text-ink-faint mt-1 text-xs">{hint}</p>
    </div>
  )
}

/**
 * Who may see what.
 *
 * Every control writes all three settings, because that is what the protocol
 * takes: a partial update cannot distinguish "nobody" from "unspecified". The
 * panel is therefore read-modify-write, and it disables while in flight rather
 * than optimistically re-rendering — showing a setting as applied before the
 * server agrees is the one mistake here that could mislead someone about what
 * they are exposing.
 */
export function PrivacyPanel() {
  const t = useTranslate()
  const { data, isPending, isError } = usePrivacy()
  const save = useSetPrivacy()

  const update = (patch: Partial<PrivacySettings>) => {
    if (!data) return
    save.mutate({ ...data, ...patch })
  }

  return (
    <section className="border-line bg-surface rounded-2xl border p-4">
      <h2 className="text-ink mb-1 text-sm font-semibold">{t('privacy.title')}</h2>
      <p className="text-ink-faint mb-2 text-xs">{t('privacy.hint')}</p>

      {isPending && (
        <p className="text-ink-faint flex items-center gap-2 text-sm">
          <Spinner className="size-4" /> {t('privacy.loading')}
        </p>
      )}
      {isError && <ErrorNote>{t('privacy.failed')}</ErrorNote>}
      {save.isError && <ErrorNote>{t('privacy.saveFailed')}</ErrorNote>}

      {data && (
        <>
          <Row
            label={t('privacy.lastSeen')}
            hint={t('privacy.lastSeenHint')}
            value={data.lastSeen}
            disabled={save.isPending}
            onChange={(lastSeen) => update({ lastSeen })}
          />
          <Row
            label={t('privacy.avatar')}
            hint={t('privacy.avatarHint')}
            value={data.avatar}
            disabled={save.isPending}
            onChange={(avatar) => update({ avatar })}
          />
          <Row
            label={t('privacy.groups')}
            hint={t('privacy.groupsHint')}
            value={data.groups}
            disabled={save.isPending}
            onChange={(groups) => update({ groups })}
          />
        </>
      )}
    </section>
  )
}
