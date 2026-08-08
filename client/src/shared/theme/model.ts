'use client'

import { create } from 'zustand'

import { readStorage, StorageKeys, writeStorage } from '../lib/storage'

export type ThemeMode = 'light' | 'dark' | 'system'

interface ThemeState {
  mode: ThemeMode
  /** what is actually painted right now — `system` resolves to one of these */
  resolved: 'light' | 'dark'
  setMode: (mode: ThemeMode) => void
  hydrate: () => () => void
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
  setMode: (mode) => {
    writeStorage(StorageKeys.theme, mode)
    set({ mode, resolved: apply(mode) })
  },
  /**
   * Applies the stored preference and keeps `system` live: returns an unsubscribe
   * so the OS-preference listener is torn down with the component that armed it.
   */
  hydrate: () => {
    const stored = readStorage<ThemeMode>(StorageKeys.theme, 'system')
    const mode: ThemeMode =
      stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system'
    set({ mode, resolved: apply(mode) })

    if (typeof window === 'undefined') return () => {}
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => {
      if (get().mode === 'system') set({ resolved: apply('system') })
    }
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  },
}))
