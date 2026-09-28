import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  formatDateSeparator,
  formatLastSeen,
  formatListTimestamp,
  formatTime,
  isDifferentDay,
} from './format'

/**
 * Every assertion here pins a *relative* fact (same string for the same input,
 * different string for a different day) rather than an exact rendering. Intl
 * output varies by ICU version and by the host's timezone database, so
 * asserting "14:03" would make the suite fail on a machine that is merely
 * configured differently — which is noise, not a regression.
 */

const NOON = new Date('2026-08-06T12:00:00').getTime()

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-08-06T15:30:00'))
})

afterEach(() => vi.useRealTimers())

describe('formatTime', () => {
  it('renders hours and minutes zero-padded', () => {
    expect(formatTime(new Date('2026-08-06T04:03:00').getTime(), 'en')).toMatch(/\b04[:.]03\b/)
  })

  it('is stable for the same instant', () => {
    expect(formatTime(NOON, 'ru')).toBe(formatTime(NOON, 'ru'))
  })

  it('renders the same instant differently per locale', () => {
    // en-US uses a 12-hour clock with a day period, ru-RU a 24-hour one. If both
    // produced the same string the locale argument would be doing nothing.
    expect(formatTime(new Date('2026-08-06T18:05:00').getTime(), 'en')).not.toBe(
      formatTime(new Date('2026-08-06T18:05:00').getTime(), 'ru'),
    )
  })
})

describe('formatListTimestamp', () => {
  /**
   * A chat row shows one short stamp in a fixed-width column. The three tiers
   * (time / weekday / date) exist so that column never has to hold a full
   * datetime, and each tier has to actually trigger at its boundary.
   */
  it('returns an empty string for a chat that has never had a message', () => {
    // Chats created but not yet written to carry lastMessageAt === 0. Formatting
    // that would render the Unix epoch — a 1970 date in the chat list.
    expect(formatListTimestamp(0, 'ru')).toBe('')
  })

  it('shows the clock time for today', () => {
    expect(formatListTimestamp(NOON, 'ru')).toBe(formatTime(NOON, 'ru'))
  })

  it('shows a weekday within the last week', () => {
    const threeDaysAgo = Date.now() - 3 * 86_400_000
    const weekday = new Intl.DateTimeFormat('en-US', { weekday: 'short' }).format(
      new Date(threeDaysAgo),
    )
    expect(formatListTimestamp(threeDaysAgo, 'en')).toBe(weekday)
  })

  it('shows a day/month date beyond a week', () => {
    const longAgo = Date.now() - 30 * 86_400_000
    const result = formatListTimestamp(longAgo, 'en')
    expect(result).toMatch(/^\d{2}\D\d{2}$/)
  })

  it('treats yesterday as a different day even minutes apart', () => {
    // 23:50 yesterday and 00:10 today are 20 minutes apart but must not collapse
    // into the "today" tier, or a stale row would read as fresh.
    vi.setSystemTime(new Date('2026-08-06T00:10:00'))
    const lateYesterday = new Date('2026-08-05T23:50:00').getTime()
    expect(formatListTimestamp(lateYesterday, 'en')).not.toBe(formatTime(lateYesterday, 'en'))
  })

  it('falls back to a date for a timestamp exactly seven days old', () => {
    const sevenDays = Date.now() - 7 * 86_400_000
    expect(formatListTimestamp(sevenDays, 'en')).toMatch(/^\d{2}\D\d{2}$/)
  })
})

describe('formatDateSeparator', () => {
  it('includes the year so old history is unambiguous', () => {
    expect(formatDateSeparator(NOON, 'en')).toContain('2026')
  })

  it('spells the month out rather than numbering it', () => {
    expect(formatDateSeparator(NOON, 'en')).toMatch(/August/i)
  })

  it('localises the month name', () => {
    expect(formatDateSeparator(NOON, 'ru')).not.toBe(formatDateSeparator(NOON, 'en'))
  })
})

describe('isDifferentDay', () => {
  /**
   * Drives the day separators in the message list. A false negative merges two
   * days into one block; a false positive inserts a separator mid-conversation.
   */
  it('is false within one calendar day', () => {
    expect(
      isDifferentDay(
        new Date('2026-08-06T00:00:01').getTime(),
        new Date('2026-08-06T23:59:59').getTime(),
      ),
    ).toBe(false)
  })

  it('is true across midnight even one second apart', () => {
    expect(
      isDifferentDay(
        new Date('2026-08-06T23:59:59').getTime(),
        new Date('2026-08-07T00:00:00').getTime(),
      ),
    ).toBe(true)
  })

  it('is true for the same day number in a different month', () => {
    // A naive getDate()-only comparison would call these equal and drop the
    // separator between July and August.
    expect(
      isDifferentDay(
        new Date('2026-07-06T12:00:00').getTime(),
        new Date('2026-08-06T12:00:00').getTime(),
      ),
    ).toBe(true)
  })

  it('is true for the same day and month in a different year', () => {
    expect(
      isDifferentDay(
        new Date('2025-08-06T12:00:00').getTime(),
        new Date('2026-08-06T12:00:00').getTime(),
      ),
    ).toBe(true)
  })

  it('is symmetric', () => {
    const a = new Date('2026-08-06T12:00:00').getTime()
    const b = new Date('2026-08-07T12:00:00').getTime()
    expect(isDifferentDay(a, b)).toBe(isDifferentDay(b, a))
  })
})

describe('formatLastSeen', () => {
  it('returns an empty string when presence was never reported', () => {
    expect(formatLastSeen(0, 'en')).toBe('')
  })

  it('says "just now" under a minute', () => {
    expect(formatLastSeen(Date.now() - 30_000, 'en')).toBe('just now')
    expect(formatLastSeen(Date.now() - 30_000, 'ru')).toBe('только что')
  })

  it('counts whole minutes under an hour', () => {
    expect(formatLastSeen(Date.now() - 5 * 60_000, 'en')).toBe('5m ago')
    expect(formatLastSeen(Date.now() - 5 * 60_000, 'ru')).toBe('5 мин назад')
  })

  it('floors partial minutes rather than rounding up', () => {
    // 59m59s must not read as "60m ago"; the hour tier owns that.
    expect(formatLastSeen(Date.now() - (59 * 60_000 + 59_000), 'en')).toBe('59m ago')
  })

  it('switches to a calendar stamp past an hour', () => {
    const twoHoursAgo = Date.now() - 2 * 3_600_000
    expect(formatLastSeen(twoHoursAgo, 'en')).toBe(formatListTimestamp(twoHoursAgo, 'en'))
  })

  it('never renders a negative age for a clock-skewed future timestamp', () => {
    // Server timestamps can land slightly ahead of a client whose clock lags.
    // "-3m ago" would look broken; "just now" is the honest degradation.
    expect(formatLastSeen(Date.now() + 3 * 60_000, 'en')).toBe('just now')
  })
})
