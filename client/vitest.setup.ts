import { webcrypto } from 'node:crypto'

import { IDBFactory } from 'fake-indexeddb'
import { beforeEach } from 'vitest'

import { resetVaultForTests } from '@/shared/lib/vault'

/**
 * Browser APIs jsdom does not implement, supplied for the vault.
 *
 * jsdom has no IndexedDB at all and a `crypto` object without `subtle`, so
 * `shared/lib/vault` would take its "unavailable" branch in every test and the
 * encrypted-storage path — the one that now holds the private keys — would
 * never be exercised. Both substitutes are real implementations rather than
 * stubs: `fake-indexeddb` is the reference IndexedDB test double, and the
 * WebCrypto here is Node's own, so AES-GCM really encrypts and really fails to
 * decrypt under the wrong key.
 */
if (typeof globalThis.crypto === 'undefined') {
  Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true })
} else if (typeof globalThis.crypto.subtle === 'undefined') {
  // jsdom's crypto has getRandomValues but no subtle, and the property is
  // read-only, so it has to be redefined rather than assigned.
  Object.defineProperty(globalThis.crypto, 'subtle', {
    value: webcrypto.subtle,
    configurable: true,
  })
}

beforeEach(() => {
  // A fresh factory per test, rather than clearing the stores: a test that
  // leaves a connection open would otherwise block the next one's upgrade, and
  // the failure would land in whichever test happened to run after it.
  globalThis.indexedDB = new IDBFactory()
  resetVaultForTests()
})
