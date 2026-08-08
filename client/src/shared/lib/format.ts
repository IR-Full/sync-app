import type { Locale } from '../i18n/locales'

const LOCALE_TAGS: Record<Locale, string> = {
  ru: 'ru-RU',
  en: 'en-US',
}

/** Clock time, e.g. "14:03". */
export function formatTime(timestampMs: number, locale: Locale): string {
  return new Intl.DateTimeFormat(LOCALE_TAGS[locale], {
    hour: '2-digit',
    minute: '2-digit',
  }).format(timestampMs)
}

/** Compact stamp for a chat-list row: time today, weekday this week, else a date. */
export function formatListTimestamp(timestampMs: number, locale: Locale): string {
  if (!timestampMs) return ''
  const tag = LOCALE_TAGS[locale]
  const date = new Date(timestampMs)
  const now = new Date()
  const sameDay =
    date.getDate() === now.getDate() &&
    date.getMonth() === now.getMonth() &&
    date.getFullYear() === now.getFullYear()

  if (sameDay) return formatTime(timestampMs, locale)

  const daysAgo = (now.getTime() - timestampMs) / 86_400_000
  if (daysAgo < 7) {
    return new Intl.DateTimeFormat(tag, { weekday: 'short' }).format(date)
  }
  return new Intl.DateTimeFormat(tag, { day: '2-digit', month: '2-digit' }).format(date)
}

/** Day separator inside a chat, e.g. "6 August 2026". */
export function formatDateSeparator(timestampMs: number, locale: Locale): string {
  return new Intl.DateTimeFormat(LOCALE_TAGS[locale], {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(timestampMs)
}

/** True when two timestamps fall on different calendar days. */
export function isDifferentDay(a: number, b: number): boolean {
  const first = new Date(a)
  const second = new Date(b)
  return (
    first.getDate() !== second.getDate() ||
    first.getMonth() !== second.getMonth() ||
    first.getFullYear() !== second.getFullYear()
  )
}

/** "last seen" line for a direct chat header. */
export function formatLastSeen(timestampMs: number, locale: Locale): string {
  if (!timestampMs) return ''
  const minutesAgo = (Date.now() - timestampMs) / 60_000
  if (minutesAgo < 1) return locale === 'ru' ? 'только что' : 'just now'
  if (minutesAgo < 60) {
    const value = Math.floor(minutesAgo)
    return locale === 'ru' ? `${value} мин назад` : `${value}m ago`
  }
  return formatListTimestamp(timestampMs, locale)
}
