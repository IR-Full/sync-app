'use client'

import { create } from 'zustand'

import { readStorage, StorageKeys, writeStorage } from '../lib/storage'

export type ThemeMode = 'light' | 'dark' | 'system'

/**
 * Accent palettes, orthogonal to light/dark.
 *
 * `default` is the built-in blue and is the only one a free account gets; the
 * rest are a Premium entitlement (`customThemes`). The gate lives in the UI
 * rather than here, because this store is also what RESTORES a stored accent on
 * load — an account whose subscription lapsed should stop being offered the
 * picker without its current colour changing under it mid-session.
 */
export const ACCENTS = ['default', 'violet', 'emerald', 'amber', 'rose'] as const
export type Accent = (typeof ACCENTS)[number]

export function isAccent(value: unknown): value is Accent {
  return typeof value === 'string' && (ACCENTS as readonly string[]).includes(value)
}

interface ThemeState {
  mode: ThemeMode
  /** what is actually painted right now — `system` resolves to one of these */
  resolved: 'light' | 'dark'
  accent: Accent
  setMode: (mode: ThemeMode) => void
  setAccent: (accent: Accent) => void
  hydrate: () => () => void
}

/**
 * Writes the accent onto <html>, where the CSS variable blocks key off it.
 *
 * `default` REMOVES the attribute rather than setting it to "default": the
 * built-in palette lives on bare `:root`, so an attribute selector for it would
 * have to duplicate those values and then drift from them.
 */
function applyAccent(accent: Accent): Accent {
  if (typeof document !== 'undefined') {
    if (accent === 'default') delete document.documentElement.dataset.accent
    else document.documentElement.dataset.accent = accent
  }
  return accent
}

function systemPrefersDark(): boolean {
  if (typeof window === 'undefined') return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function apply(mode: ThemeMode): 'light' | 'dark' {
  const resolved = mode === 'system' ? (systemPrefersDark() ? 'dark' : 'light') : mode
  if (typeof document !== 'undefined') {
    document.documentElement.classList.toggle('dark', resolved === 'dark')
    document.documentElement.style.colorScheme = resolved
  }
  return resolved
}

export const useThemeStore = create<ThemeState>((set, get) => ({
  mode: 'system',
  resolved: 'light',
  accent: 'default',
  setMode: (mode) => {
    writeStorage(StorageKeys.theme, mode)
    set({ mode, resolved: apply(mode) })
  },
  setAccent: (accent) => {
    writeStorage(StorageKeys.accent, accent)
    set({ accent: applyAccent(accent) })
  },
  /**
   * Applies the stored preference and keeps `system` live: returns an unsubscribe
   * so the OS-preference listener is torn down with the component that armed it.
   */
  hydrate: () => {
    const stored = readStorage<ThemeMode>(StorageKeys.theme, 'system')
    const mode: ThemeMode =
      stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system'
    const storedAccent = readStorage<Accent>(StorageKeys.accent, 'default')
    const accent: Accent = isAccent(storedAccent) ? storedAccent : 'default'
    set({ mode, resolved: apply(mode), accent: applyAccent(accent) })

    if (typeof window === 'undefined') return () => {}
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => {
      if (get().mode === 'system') set({ resolved: apply('system') })
    }
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  },
}))
