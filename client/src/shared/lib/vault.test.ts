import { describe, expect, it } from 'vitest'

import { vaultAvailable, vaultClear, vaultRead, vaultRemove, vaultWrite } from './vault'

describe('vaultWrite / vaultRead', () => {
  it('round-trips a value', async () => {
    await vaultWrite('k', { a: 1, b: 'two' })
    expect(await vaultRead('k', null)).toEqual({ a: 1, b: 'two' })
  })

  it('returns the fallback for a key never written', async () => {
    expect(await vaultRead('absent', 'fallback')).toBe('fallback')
  })

  it('keeps values under separate keys apart', async () => {
    await vaultWrite('a', 1)
    await vaultWrite('b', 2)
    expect(await vaultRead('a', 0)).toBe(1)
    expect(await vaultRead('b', 0)).toBe(2)
  })

  it('overwrites rather than accumulating', async () => {
    await vaultWrite('k', 'first')
    await vaultWrite('k', 'second')
    expect(await vaultRead('k', null)).toBe('second')
  })

  /**
   * The point of the whole module. If the stored record were the plaintext, or
   * anything reversible without the key, moving the keys out of localStorage
   * would have bought nothing.
   */
  it('stores ciphertext, not the value', async () => {
    await vaultWrite('k', { secret: 'attack at dawn' })

    const raw = await new Promise<{ iv: unknown; data: ArrayBuffer }>((resolve, reject) => {
      const open = indexedDB.open('SyncApp', 1)
      open.onsuccess = () => {
        const request = open.result
          .transaction('vault', 'readonly')
          .objectStore('vault')
          .get('k')
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      }
      open.onerror = () => reject(open.error)
    })

    const bytes = new Uint8Array(raw.data)
    expect(new TextDecoder().decode(bytes)).not.toContain('attack at dawn')
    // AES-GCM appends a 16-byte tag, so the record is longer than the plaintext
    // as well as unreadable — a record the same length would suggest a cipher
    // that authenticates nothing.
    expect(bytes.byteLength).toBeGreaterThan(
      new TextEncoder().encode(JSON.stringify({ secret: 'attack at dawn' })).byteLength,
    )
  })

  /**
   * Reusing an IV under one AES-GCM key leaks the XOR of the two plaintexts and
   * lets an attacker forge messages. Writing the same value twice is the
   * cheapest way to notice a fresh IV is not being generated.
   */
  it('uses a fresh IV per write', async () => {
    const ivs = new Set<string>()
    for (let i = 0; i < 5; i += 1) {
      await vaultWrite('k', 'same value every time')
      const raw = await new Promise<{ iv: Uint8Array }>((resolve, reject) => {
        const open = indexedDB.open('SyncApp', 1)
        open.onsuccess = () => {
          const request = open.result
            .transaction('vault', 'readonly')
            .objectStore('vault')
            .get('k')
          request.onsuccess = () => resolve(request.result)
          request.onerror = () => reject(request.error)
        }
        open.onerror = () => reject(open.error)
      })
      ivs.add(Array.from(new Uint8Array(raw.iv.buffer ?? raw.iv)).join(','))
    }
    expect(ivs.size).toBe(5)
  })

  it('reads a value back as absent when its record was tampered with', async () => {
    await vaultWrite('k', { a: 1 })

    await new Promise<void>((resolve, reject) => {
      const open = indexedDB.open('SyncApp', 1)
      open.onsuccess = () => {
        const store = open.result.transaction('vault', 'readwrite').objectStore('vault')
        const read = store.get('k')
        read.onsuccess = () => {
          const record = read.result as { iv: Uint8Array; data: ArrayBuffer }
          const flipped = new Uint8Array(record.data)
          flipped[0] ^= 0xff
          const write = store.put({ iv: record.iv, data: flipped.buffer }, 'k')
          write.onsuccess = () => resolve()
          write.onerror = () => reject(write.error)
        }
        read.onerror = () => reject(read.error)
      }
      open.onerror = () => reject(open.error)
    })

    // Not a throw and not garbage: AES-GCM authenticates, so a flipped bit
    // fails to open and the caller sees "nothing stored".
    expect(await vaultRead('k', 'fallback')).toBe('fallback')
  })
})

describe('vaultRemove / vaultClear', () => {
  it('forgets one key', async () => {
    await vaultWrite('a', 1)
    await vaultWrite('b', 2)
    await vaultRemove('a')

    expect(await vaultRead('a', null)).toBeNull()
    expect(await vaultRead('b', null)).toBe(2)
  })

  it('forgets everything', async () => {
    await vaultWrite('a', 1)
    await vaultWrite('b', 2)
    await vaultClear()

    expect(await vaultRead('a', null)).toBeNull()
    expect(await vaultRead('b', null)).toBeNull()
  })

  /**
   * Clearing on logout must leave the wrapping key in place. Destroying it
   * would orphan nothing today, but a second key in the store is a second thing
   * that can be picked up on the next read, and only one of them opens anything.
   */
  it('keeps writing after a clear', async () => {
    await vaultWrite('a', 1)
    await vaultClear()
    await vaultWrite('a', 2)

    expect(await vaultRead('a', null)).toBe(2)
  })
})

describe('vaultAvailable', () => {
  it('is true where IndexedDB and crypto.subtle both exist', () => {
    expect(vaultAvailable()).toBe(true)
  })
})
