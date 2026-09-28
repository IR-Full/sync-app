import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StorageKeys, readStorage, removeStorage, writeStorage } from './storage'

beforeEach(() => localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe('readStorage / writeStorage', () => {
  it('round-trips a value', () => {
    writeStorage('k', { a: 1, b: 'two' })
    expect(readStorage('k', null)).toEqual({ a: 1, b: 'two' })
  })

  it('returns the fallback for a missing key', () => {
    expect(readStorage('never-written', 'fallback')).toBe('fallback')
  })

  it('distinguishes a stored null from a missing key', () => {
    writeStorage('k', null)
    expect(readStorage('k', 'fallback')).toBeNull()
  })

  it('round-trips primitives and arrays', () => {
    writeStorage('num', 42)
    writeStorage('bool', false)
    writeStorage('list', [1, 2, 3])

    expect(readStorage('num', 0)).toBe(42)
    expect(readStorage('bool', true)).toBe(false)
    expect(readStorage('list', [])).toEqual([1, 2, 3])
  })

  /**
   * Storage fails in more situations than it looks: Safari private mode, quota
   * exhaustion, disabled cookies. Every caller is on a UI path, so a failure
   * has to degrade quietly rather than blank the screen.
   */
  it('returns the fallback rather than throwing on corrupt JSON', () => {
    localStorage.setItem('corrupt', '{not valid json')
    expect(readStorage('corrupt', 'safe')).toBe('safe')
  })

  it('survives a throwing getItem', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    expect(readStorage('k', 'safe')).toBe('safe')
  })

  it('survives a throwing setItem (quota exceeded)', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    expect(() => writeStorage('k', 'value')).not.toThrow()
  })

  it('survives a throwing removeItem', () => {
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    expect(() => removeStorage('k')).not.toThrow()
  })
})

describe('removeStorage', () => {
  it('deletes a stored value', () => {
    writeStorage('k', 'value')
    removeStorage('k')
    expect(readStorage('k', 'gone')).toBe('gone')
  })

  it('is a no-op for a key that was never written', () => {
    expect(() => removeStorage('never')).not.toThrow()
  })
})

describe('StorageKeys', () => {
  it('namespaces every key, so nothing collides with another app on the origin', () => {
    for (const key of Object.values(StorageKeys)) {
      expect(key.startsWith('SyncApp:')).toBe(true)
    }
  })

  it('keeps the keys distinct', () => {
    const values = Object.values(StorageKeys)
    expect(new Set(values).size).toBe(values.length)
  })
})
