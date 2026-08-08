'use client'

import { useCallback } from 'react'
import { create } from 'zustand'

import { readStorage, StorageKeys, writeStorage } from '../lib/storage'
import { translate, type TranslationKey } from './dictionaries'
import { DEFAULT_LOCALE, detectLocale, isLocale, type Locale } from './locales'

interface LocaleState {
  locale: Locale
  /** false until the stored/detected preference is applied, so SSR and the first client render agree */
  hydrated: boolean
  setLocale: (locale: Locale) => void
  hydrate: () => void
}

/**
 * Locale lives in Zustand rather than a React context because it is pure client
 * state: nothing on the server knows or cares about it, and stores let a leaf
 * component subscribe without threading a provider through every layer.
 */
export const useLocaleStore = create<LocaleState>((set) => ({
  locale: DEFAULT_LOCALE,
  hydrated: false,
  setLocale: (locale) => {
    writeStorage(StorageKeys.locale, locale)
    if (typeof document !== 'undefined') document.documentElement.lang = locale
    set({ locale })
  },
  hydrate: () => {
    const stored = readStorage<string>(StorageKeys.locale, '')
    const locale = isLocale(stored) ? stored : detectLocale()
    if (typeof document !== 'undefined') document.documentElement.lang = locale
    set({ locale, hydrated: true })
  },
}))

export type TranslateFn = (
  key: TranslationKey,
  params?: Record<string, string | number>,
) => string

/** Translation function bound to the active locale. */
export function useTranslate(): TranslateFn {
  const locale = useLocaleStore((state) => state.locale)
  return useCallback(
    (key: TranslationKey, params?: Record<string, string | number>) =>
      translate(locale, key, params),
    [locale],
  )
}

export function useLocale(): Locale {
  return useLocaleStore((state) => state.locale)
}
