import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StorageKeys, readStorage } from '../lib/storage'
import { useThemeStore, type ThemeMode } from './model'

/**
 * jsdom implements `matchMedia` as a stub with no listener support and `matches`
 * hard-wired to false, so the OS-preference half of this store is invisible
 * without a replacement that can actually change its mind.
 */
function stubMatchMedia(prefersDark: boolean) {
  const listeners = new Set<() => void>()
  const media = {
    matches: prefersDark,
    addEventListener: (_: string, handler: () => void) => listeners.add(handler),
    removeEventListener: (_: string, handler: () => void) => listeners.delete(handler),
  }
  vi.stubGlobal('matchMedia', () => media)
  return {
    media,
    listenerCount: () => listeners.size,
    /** The user switched their OS between light and dark. */
    change(nowDark: boolean) {
      media.matches = nowDark
      for (const handler of listeners) handler()
    },
  }
}

const store = () => useThemeStore.getState()
const root = () => document.documentElement

beforeEach(() => {
  localStorage.clear()
  root().classList.remove('dark')
  root().style.colorScheme = ''
  useThemeStore.setState({ mode: 'system', resolved: 'light' })
})

afterEach(() => vi.unstubAllGlobals())

describe('initial state', () => {
  it('defaults to following the system', () => {
    expect(store().mode).toBe('system')
  })

  it('reports light until hydrate resolves the real preference', () => {
    // SSR has no `window.matchMedia`, so the server must render *something*.
    // Reading storage at module scope instead would cause a hydration mismatch.
    expect(store().resolved).toBe('light')
  })
})

describe('setMode', () => {
  it('records an explicit choice', () => {
    stubMatchMedia(false)
    store().setMode('dark')
    expect(store().mode).toBe('dark')
    expect(store().resolved).toBe('dark')
  })

  it('persists the choice', () => {
    stubMatchMedia(false)
    store().setMode('dark')
    expect(readStorage<ThemeMode>(StorageKeys.theme, 'system')).toBe('dark')
  })

  it('adds the dark class Tailwind keys its variants on', () => {
    stubMatchMedia(false)
    store().setMode('dark')
    expect(root().classList.contains('dark')).toBe(true)
  })

  it('removes the dark class when switching back to light', () => {
    stubMatchMedia(false)
    store().setMode('dark')
    store().setMode('light')
    expect(root().classList.contains('dark')).toBe(false)
  })

  it('sets color-scheme so form controls and scrollbars follow', () => {
    // Without it the browser paints native widgets light on a dark page.
    stubMatchMedia(false)
    store().setMode('dark')
    expect(root().style.colorScheme).toBe('dark')
  })

  it('resolves system against the OS preference', () => {
    stubMatchMedia(true)
    store().setMode('system')
    expect(store().resolved).toBe('dark')
  })

  it('an explicit choice overrides the OS preference', () => {
    stubMatchMedia(true)
    store().setMode('light')
    expect(store().resolved).toBe('light')
    expect(root().classList.contains('dark')).toBe(false)
  })
})

describe('hydrate', () => {
  it('restores a stored explicit mode', () => {
    stubMatchMedia(false)
    localStorage.setItem(StorageKeys.theme, JSON.stringify('dark'))
    store().hydrate()

    expect(store().mode).toBe('dark')
    expect(root().classList.contains('dark')).toBe(true)
  })

  it('defaults to system when nothing is stored', () => {
    stubMatchMedia(true)
    store().hydrate()

    expect(store().mode).toBe('system')
    expect(store().resolved).toBe('dark')
  })

  it('ignores a stored value that is not a mode', () => {
    // A downgrade or a hand-edited localStorage must not leave the app in a mode
    // that resolves to neither palette.
    stubMatchMedia(false)
    localStorage.setItem(StorageKeys.theme, JSON.stringify('solarized'))
    store().hydrate()

    expect(store().mode).toBe('system')
  })

  it('falls back to system on corrupt storage', () => {
    stubMatchMedia(false)
    localStorage.setItem(StorageKeys.theme, '{not json')
    expect(() => store().hydrate()).not.toThrow()
    expect(store().mode).toBe('system')
  })

  it('returns an unsubscribe function', () => {
    stubMatchMedia(false)
    expect(typeof store().hydrate()).toBe('function')
  })
})

describe('following the OS preference', () => {
  /**
   * `system` has to stay live: a user who switches their OS to dark at sunset
   * expects the open tab to follow, not to need a reload.
   */
  it('repaints when the OS preference changes', () => {
    const media = stubMatchMedia(false)
    store().hydrate()
    expect(store().resolved).toBe('light')

    media.change(true)

    expect(store().resolved).toBe('dark')
    expect(root().classList.contains('dark')).toBe(true)
  })

  it('ignores the OS once the user has chosen explicitly', () => {
    // An explicit choice is a choice; overriding it at sunset would be a bug the
    // user cannot work around.
    const media = stubMatchMedia(false)
    store().hydrate()
    store().setMode('light')

    media.change(true)

    expect(store().resolved).toBe('light')
    expect(root().classList.contains('dark')).toBe(false)
  })

  it('follows the OS again after switching back to system', () => {
    const media = stubMatchMedia(false)
    store().hydrate()
    store().setMode('dark')
    store().setMode('system')

    media.change(true)
    expect(store().resolved).toBe('dark')
  })

  it('stops listening once unsubscribed', () => {
    // The listener is armed by a component effect. Leaking it would keep a
    // reference to the store alive per mount and repaint after unmount.
    const media = stubMatchMedia(false)
    const unsubscribe = store().hydrate()
    expect(media.listenerCount()).toBe(1)

    unsubscribe()

    expect(media.listenerCount()).toBe(0)
    media.change(true)
    expect(store().resolved).toBe('light')
  })

  it('arms exactly one listener per hydrate', () => {
    const media = stubMatchMedia(false)
    const first = store().hydrate()
    const second = store().hydrate()
    expect(media.listenerCount()).toBe(2)

    first()
    second()
    expect(media.listenerCount()).toBe(0)
  })
})
