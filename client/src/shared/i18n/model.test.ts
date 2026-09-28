import { renderHook } from '@testing-library/react'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StorageKeys } from '../lib/storage'
import { dictionaries } from './dictionaries'
import { DEFAULT_LOCALE } from './locales'
import { useLocale, useLocaleStore, useTranslate } from './model'

const initial = useLocaleStore.getState()

beforeEach(() => {
  localStorage.clear()
  document.documentElement.lang = ''
  useLocaleStore.setState({ locale: initial.locale, hydrated: false })
})

afterEach(() => vi.unstubAllGlobals())

describe('useLocaleStore', () => {
  it('starts on the default locale and unhydrated', () => {
    // SSR renders with these values. If the store read localStorage at module
    // scope instead, the server and the first client render would disagree and
    // React would throw a hydration mismatch.
    expect(useLocaleStore.getState().locale).toBe(DEFAULT_LOCALE)
    expect(useLocaleStore.getState().hydrated).toBe(false)
  })

  describe('setLocale', () => {
    it('updates the active locale', () => {
      act(() => useLocaleStore.getState().setLocale('en'))
      expect(useLocaleStore.getState().locale).toBe('en')
    })

    it('persists the choice so it survives a reload', () => {
      act(() => useLocaleStore.getState().setLocale('en'))
      expect(localStorage.getItem(StorageKeys.locale)).toBe(JSON.stringify('en'))
    })

    it('mirrors the locale onto <html lang> for screen readers and hyphenation', () => {
      act(() => useLocaleStore.getState().setLocale('en'))
      expect(document.documentElement.lang).toBe('en')
    })
  })

  describe('hydrate', () => {
    it('restores a stored preference over the browser default', () => {
      localStorage.setItem(StorageKeys.locale, JSON.stringify('en'))
      act(() => useLocaleStore.getState().hydrate())
      expect(useLocaleStore.getState().locale).toBe('en')
    })

    it('marks the store hydrated', () => {
      act(() => useLocaleStore.getState().hydrate())
      expect(useLocaleStore.getState().hydrated).toBe(true)
    })

    it('detects from the browser when nothing is stored', () => {
      vi.stubGlobal('navigator', { languages: ['en-US'], language: 'en-US' })
      act(() => useLocaleStore.getState().hydrate())
      expect(useLocaleStore.getState().locale).toBe('en')
    })

    it('ignores a stored value that is not a shipped locale', () => {
      // A downgrade, or a locale removed from the build, must not leave the app
      // indexing dictionaries with a key that no longer exists.
      localStorage.setItem(StorageKeys.locale, JSON.stringify('klingon'))
      vi.stubGlobal('navigator', { languages: ['ru-RU'], language: 'ru-RU' })
      act(() => useLocaleStore.getState().hydrate())
      expect(useLocaleStore.getState().locale).toBe('ru')
    })

    it('sets <html lang> on hydrate, not only on an explicit change', () => {
      localStorage.setItem(StorageKeys.locale, JSON.stringify('en'))
      act(() => useLocaleStore.getState().hydrate())
      expect(document.documentElement.lang).toBe('en')
    })

    it('is idempotent', () => {
      localStorage.setItem(StorageKeys.locale, JSON.stringify('en'))
      act(() => useLocaleStore.getState().hydrate())
      act(() => useLocaleStore.getState().hydrate())
      expect(useLocaleStore.getState().locale).toBe('en')
    })
  })
})

describe('useTranslate', () => {
  it('translates in the active locale', () => {
    act(() => useLocaleStore.getState().setLocale('en'))
    const { result } = renderHook(() => useTranslate())
    expect(result.current('nav.chats')).toBe(dictionaries.en['nav.chats'])
  })

  it('re-renders with a new function when the locale changes', () => {
    // The callback is memoised on `locale`. If the identity were stable across a
    // locale change, every `useMemo`/`React.memo` downstream would keep showing
    // the previous language until something unrelated forced a re-render.
    act(() => useLocaleStore.getState().setLocale('ru'))
    const { result } = renderHook(() => useTranslate())
    const before = result.current

    act(() => useLocaleStore.getState().setLocale('en'))

    expect(result.current).not.toBe(before)
    expect(result.current('nav.chats')).toBe(dictionaries.en['nav.chats'])
  })

  it('keeps a stable identity while the locale holds', () => {
    const { result, rerender } = renderHook(() => useTranslate())
    const before = result.current
    rerender()
    expect(result.current).toBe(before)
  })
})

describe('useLocale', () => {
  it('reports the active locale', () => {
    act(() => useLocaleStore.getState().setLocale('en'))
    const { result } = renderHook(() => useLocale())
    expect(result.current).toBe('en')
  })

  it('tracks later changes', () => {
    const { result } = renderHook(() => useLocale())
    act(() => useLocaleStore.getState().setLocale('en'))
    expect(result.current).toBe('en')
  })
})
