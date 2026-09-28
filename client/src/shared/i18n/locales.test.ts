import { afterEach, describe, expect, it, vi } from 'vitest'

import { DEFAULT_LOCALE, LOCALES, LOCALE_LABELS, detectLocale, isLocale } from './locales'

afterEach(() => vi.unstubAllGlobals())

/** Replaces `navigator.languages` for one test without touching the real object. */
function stubLanguages(languages: string[]): void {
  vi.stubGlobal('navigator', { languages, language: languages[0] })
}

describe('LOCALES', () => {
  it('has a label for every shipped locale', () => {
    // The settings panel renders one row per locale from LOCALE_LABELS. A locale
    // added to LOCALES without a label would render `undefined` in the picker.
    for (const locale of LOCALES) {
      expect(LOCALE_LABELS[locale]).toBeTruthy()
    }
  })

  it('includes the default locale', () => {
    expect(LOCALES).toContain(DEFAULT_LOCALE)
  })

  it('lists no duplicates', () => {
    expect(new Set(LOCALES).size).toBe(LOCALES.length)
  })
})

describe('isLocale', () => {
  it('accepts every shipped locale', () => {
    for (const locale of LOCALES) expect(isLocale(locale)).toBe(true)
  })

  it('rejects an unshipped language tag', () => {
    expect(isLocale('de')).toBe(false)
  })

  it('rejects a region-qualified tag', () => {
    // Callers must strip the region first; accepting 'en-GB' here would index
    // the dictionaries with a key that does not exist.
    expect(isLocale('en-GB')).toBe(false)
  })

  it('rejects non-strings without throwing', () => {
    // It guards values read back out of localStorage, which can hold anything a
    // previous version — or a user with devtools open — put there.
    expect(isLocale(null)).toBe(false)
    expect(isLocale(undefined)).toBe(false)
    expect(isLocale(42)).toBe(false)
    expect(isLocale({ toString: () => 'en' })).toBe(false)
    expect(isLocale(['en'])).toBe(false)
  })
})

describe('detectLocale', () => {
  it('matches an exact language tag', () => {
    stubLanguages(['en'])
    expect(detectLocale()).toBe('en')
  })

  it('strips the region before matching', () => {
    // Browsers almost always report a region ('en-US', 'ru-RU'); an exact-match
    // lookup would miss every real browser and always return the default.
    stubLanguages(['en-GB'])
    expect(detectLocale()).toBe('en')
  })

  it('honours the order of the preference list', () => {
    stubLanguages(['en-US', 'ru-RU'])
    expect(detectLocale()).toBe('en')
  })

  it('skips languages we do not ship and takes the first we do', () => {
    stubLanguages(['de-DE', 'fr-FR', 'ru-RU'])
    expect(detectLocale()).toBe('ru')
  })

  it('falls back to the default when nothing matches', () => {
    stubLanguages(['de-DE', 'ja-JP'])
    expect(detectLocale()).toBe(DEFAULT_LOCALE)
  })

  it('falls back to navigator.language when languages is absent', () => {
    // Some embedded webviews expose only the singular property.
    vi.stubGlobal('navigator', { languages: undefined, language: 'en-US' })
    expect(detectLocale()).toBe('en')
  })

  it('returns the default with an empty preference list', () => {
    stubLanguages([])
    expect(detectLocale()).toBe(DEFAULT_LOCALE)
  })
})
