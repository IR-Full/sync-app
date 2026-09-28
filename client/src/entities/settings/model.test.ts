import { beforeEach, describe, expect, it, vi } from 'vitest'

import { StorageKeys, readStorage } from '@/shared/lib/storage'
import { useSettingsStore, type Settings } from './model'

const store = () => useSettingsStore.getState()
const stored = () => readStorage<Partial<Settings>>(StorageKeys.settings, {})

beforeEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
  store().hydrate()
})

describe('defaults', () => {
  /**
   * Both notification toggles default off and read receipts default on, matching
   * what the user already agreed to by installing a messenger: the browser gates
   * notifications behind its own permission prompt anyway, and receipts are
   * reciprocal — turning ours off also hides other people's from us.
   */
  it('starts with notifications and sound off', () => {
    expect(store().desktopNotifications).toBe(false)
    expect(store().soundOnMessage).toBe(false)
  })

  it('starts with read receipts on', () => {
    expect(store().sendReadReceipts).toBe(true)
  })
})

describe('set', () => {
  it('updates a flag', () => {
    store().set('soundOnMessage', true)
    expect(store().soundOnMessage).toBe(true)
  })

  it('persists the change', () => {
    store().set('soundOnMessage', true)
    expect(stored().soundOnMessage).toBe(true)
  })

  it('persists a flag turned off, not just one turned on', () => {
    // `sendReadReceipts` defaults to true, so a persistence bug that only wrote
    // truthy values would silently ignore the one setting with a privacy effect.
    store().set('sendReadReceipts', false)
    expect(stored().sendReadReceipts).toBe(false)
  })

  it('persists every flag, not only the one that changed', () => {
    // Otherwise each write would drop the other two back to their defaults on
    // the next reload.
    store().set('soundOnMessage', true)
    store().set('desktopNotifications', true)

    expect(stored()).toMatchObject({
      soundOnMessage: true,
      desktopNotifications: true,
      sendReadReceipts: true,
    })
  })

  it('leaves the other flags alone in memory', () => {
    store().set('soundOnMessage', true)
    expect(store().desktopNotifications).toBe(false)
    expect(store().sendReadReceipts).toBe(true)
  })

  it('survives a storage failure without losing the in-memory change', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })

    expect(() => store().set('soundOnMessage', true)).not.toThrow()
    expect(store().soundOnMessage).toBe(true)
  })
})

describe('hydrate', () => {
  it('restores stored settings', () => {
    localStorage.setItem(
      StorageKeys.settings,
      JSON.stringify({ soundOnMessage: true, sendReadReceipts: false }),
    )
    store().hydrate()

    expect(store().soundOnMessage).toBe(true)
    expect(store().sendReadReceipts).toBe(false)
  })

  it('fills in a setting the stored object predates', () => {
    // A build that adds a new toggle must not read it back as undefined, which
    // would render the switch in neither position.
    localStorage.setItem(StorageKeys.settings, JSON.stringify({ soundOnMessage: true }))
    store().hydrate()

    expect(store().desktopNotifications).toBe(false)
    expect(store().sendReadReceipts).toBe(true)
  })

  it('returns to defaults when nothing is stored', () => {
    store().set('soundOnMessage', true)
    localStorage.clear()
    store().hydrate()

    expect(store().soundOnMessage).toBe(false)
  })

  it('falls back to defaults on corrupt JSON', () => {
    localStorage.setItem(StorageKeys.settings, '{not json')
    expect(() => store().hydrate()).not.toThrow()
    expect(store().sendReadReceipts).toBe(true)
  })

  it('is idempotent', () => {
    localStorage.setItem(StorageKeys.settings, JSON.stringify({ soundOnMessage: true }))
    store().hydrate()
    store().hydrate()
    expect(store().soundOnMessage).toBe(true)
  })

  it('round-trips a full set of changes through storage', () => {
    store().set('desktopNotifications', true)
    store().set('soundOnMessage', true)
    store().set('sendReadReceipts', false)
    store().hydrate()

    expect(store()).toMatchObject({
      desktopNotifications: true,
      soundOnMessage: true,
      sendReadReceipts: false,
    })
  })
})
