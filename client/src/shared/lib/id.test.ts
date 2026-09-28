import { beforeEach, describe, expect, it } from 'vitest'

import { createDedupKey, getDeviceId } from './id'
import { StorageKeys, readStorage } from './storage'

beforeEach(() => localStorage.clear())

describe('getDeviceId', () => {
  /**
   * The gateway keys sessions, multi-device delivery and E2E key bundles on the
   * device id. It mints one if we send none — but then every page reload would
   * look like a brand-new device, splitting one browser across many device rows
   * and scattering secret-chat sessions.
   */
  it('is stable across calls', () => {
    const first = getDeviceId()
    expect(getDeviceId()).toBe(first)
  })

  it('persists so it survives a reload', () => {
    const id = getDeviceId()
    expect(readStorage(StorageKeys.deviceId, '')).toBe(id)
  })

  it('reuses an id already in storage', () => {
    localStorage.setItem(StorageKeys.deviceId, JSON.stringify('web-existing'))
    expect(getDeviceId()).toBe('web-existing')
  })

  it('marks the platform in the id', () => {
    // Device ids are free-form strings server-side; the prefix is what makes a
    // row identifiable as this client in a log or an admin view.
    expect(getDeviceId()).toMatch(/^web-/)
  })

  it('mints a fresh id after storage is cleared', () => {
    const first = getDeviceId()
    localStorage.clear()
    expect(getDeviceId()).not.toBe(first)
  })
})

describe('createDedupKey', () => {
  /**
   * The server enforces uniqueness on (sender_id, dedup_key), so a retry after a
   * reconnect resolves to the stored message instead of posting twice. Two
   * different messages sharing a key would make the second one silently vanish.
   */
  it('is unique per call', () => {
    const keys = new Set(Array.from({ length: 1000 }, () => createDedupKey()))
    expect(keys.size).toBe(1000)
  })

  it('is a non-empty string', () => {
    const key = createDedupKey()
    expect(typeof key).toBe('string')
    expect(key.length).toBeGreaterThan(8)
  })

  it('does not persist anything', () => {
    // A dedup key belongs to one message, not to the installation.
    createDedupKey()
    expect(localStorage.length).toBe(0)
  })
})
