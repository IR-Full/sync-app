import { describe, expect, it } from 'vitest'

import { dictionaries, translate } from './dictionaries'
import { LOCALES } from './locales'

const placeholders = (template: string): string[] =>
  [...template.matchAll(/\{(\w+)\}/g)].map((match) => match[1]).sort()

describe('dictionaries', () => {
  /**
   * `en` is the reference shape and the others are typed against it, so key
   * parity is belt-and-braces at runtime — but the type constrains only the
   * *keys*, not whether a value was left blank or copied verbatim from English.
   */
  it('defines every key in every locale', () => {
    const reference = Object.keys(dictionaries.en).sort()
    for (const locale of LOCALES) {
      expect(Object.keys(dictionaries[locale]).sort()).toEqual(reference)
    }
  })

  it('has no blank translations', () => {
    for (const locale of LOCALES) {
      const blank = Object.entries(dictionaries[locale])
        .filter(([, value]) => value.trim() === '')
        .map(([key]) => key)
      expect(blank).toEqual([])
    }
  })

  /**
   * A translator who drops `{count}` from a string produces a label that renders
   * literally ("Selected messages" with no number) rather than crashing — the
   * kind of defect nobody notices until a user reports it.
   */
  it('keeps the same placeholders across locales', () => {
    for (const [key, reference] of Object.entries(dictionaries.en)) {
      const expected = placeholders(reference)
      for (const locale of LOCALES) {
        const actual = placeholders(dictionaries[locale][key as never])
        expect({ key, locale, names: actual }).toEqual({ key, locale, names: expected })
      }
    }
  })

  it('actually translates — ru is not a wholesale copy of en', () => {
    const identical = Object.entries(dictionaries.en).filter(
      ([key, value]) => dictionaries.ru[key as never] === value,
    )
    // Brand names, symbols and a few loanwords legitimately match; most must not.
    expect(identical.length).toBeLessThan(Object.keys(dictionaries.en).length * 0.2)
  })
})

describe('translate', () => {
  it('returns the string for the requested locale', () => {
    expect(translate('ru', 'nav.chats')).toBe(dictionaries.ru['nav.chats'])
    expect(translate('en', 'nav.chats')).toBe(dictionaries.en['nav.chats'])
  })

  it('returns the template unchanged when no params are passed', () => {
    expect(translate('en', 'nav.chats')).toBe(dictionaries.en['nav.chats'])
  })

  it('falls back to English for a key missing from the active locale', () => {
    // Guards a hand-edited dictionary: an English label beats a raw key id.
    const ru = dictionaries.ru as Record<string, string>
    const saved = ru['nav.chats']
    delete ru['nav.chats']
    try {
      expect(translate('ru', 'nav.chats')).toBe(dictionaries.en['nav.chats'])
    } finally {
      ru['nav.chats'] = saved
    }
  })

  it('returns the key itself when no locale has it', () => {
    // Renders `chats.somethingNew` in the UI — ugly, but it names the missing
    // key, which is far more actionable than a blank label.
    expect(translate('en', 'not.a.real.key' as never)).toBe('not.a.real.key')
  })
})

describe('translate placeholder substitution', () => {
  /**
   * Driven through a synthetic template because *which* real keys carry
   * placeholders changes as the UI does; the substitution rule does not.
   */
  const render = (template: string, params?: Record<string, string | number>) => {
    const en = dictionaries.en as Record<string, string>
    en['__test__'] = template
    try {
      return translate('en', '__test__' as never, params)
    } finally {
      delete en['__test__']
    }
  }

  it('replaces every occurrence of a placeholder', () => {
    expect(render('{a} and {a}', { a: 'x' })).toBe('x and x')
  })

  it('replaces several distinct placeholders', () => {
    expect(render('{a}/{b}', { a: 1, b: 2 })).toBe('1/2')
  })

  it('stringifies numbers, including zero', () => {
    expect(render('{n} left', { n: 0 })).toBe('0 left')
  })

  it('leaves an unsupplied placeholder literal rather than printing undefined', () => {
    // "Hello undefined" reads as a bug to a user; "Hello {name}" reads as a bug
    // to a developer, which is where the fix has to happen anyway.
    expect(render('Hello {name}', { other: 'x' })).toBe('Hello {name}')
  })

  it('ignores extra params', () => {
    expect(render('plain', { a: 1 })).toBe('plain')
  })

  it('does not re-scan a substituted value for further placeholders', () => {
    // Otherwise a display name of "{b}" would pull in an unrelated param.
    expect(render('{a}', { a: '{b}', b: 'nested' })).toBe('{b}')
  })
})
