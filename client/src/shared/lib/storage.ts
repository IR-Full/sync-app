/**
 * localStorage helpers that never throw.
 *
 * Storage access fails in more situations than it looks: Safari private mode,
 * disabled cookies, quota exhaustion, and server-side rendering (where there is
 * no `window` at all). Every caller here is on a UI path where a storage
 * failure should degrade quietly rather than blank the screen.
 */

export function readStorage<T>(key: string, fallback: T): T {
  if (typeof window === 'undefined') return fallback
  try {
    const raw = window.localStorage.getItem(key)
    return raw === null ? fallback : (JSON.parse(raw) as T)
  } catch {
    return fallback
  }
}

export function writeStorage(key: string, value: unknown): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // Quota or private-mode failure — the app stays usable without persistence.
  }
}

export function removeStorage(key: string): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.removeItem(key)
  } catch {
    // ignore
  }
}

export const StorageKeys = {
  session: 'synapse:session',
  deviceId: 'synapse:device-id',
  theme: 'synapse:theme',
  locale: 'synapse:locale',
  chats: 'synapse:chats',
  settings: 'synapse:settings',
} as const
