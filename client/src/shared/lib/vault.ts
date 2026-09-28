/**
 * Encrypted local storage for secret-chat key material.
 *
 * Everything here exists because of one weakness and does not fully close it,
 * so it is worth being precise about what moves.
 *
 * `localStorage` holds strings. Private keys kept there are readable by
 * anything that can touch the origin's storage: a script injected into the
 * page, a browser extension with host permissions, an errant logging call that
 * serialises storage, anyone with a moment at an unlocked machine and the
 * devtools open. The commonest XSS payload in the wild is one line that ships
 * the whole of `localStorage` somewhere; against that, the keys are gone.
 *
 * Here they are AES-GCM ciphertext in IndexedDB, under a key generated with
 * `extractable: false` and stored as a `CryptoKey` handle. The browser will
 * never hand its bytes to script — `exportKey` throws — so the key cannot be
 * copied out of the origin at all. An attacker who lands script in the page can
 * still *use* the handle to decrypt, and that is the part this does not fix: it
 * turns a one-line storage dump into an attack that has to run in the page,
 * call the vault, and exfiltrate plaintext while it has execution. That is a
 * real narrowing and not a solution, which is why the nonce-based CSP in
 * `src/proxy.ts` remains the defence that matters more.
 *
 * Deliberately hand-rolled rather than `idb` or similar: the whole surface is
 * four operations on two object stores, and a dependency in the path that holds
 * the identity keys is a dependency that can replace them.
 */

const DB_NAME = 'SyncApp'
const DB_VERSION = 1

/** Holds exactly one record: the non-extractable wrapping key. */
const KEY_STORE = 'wrapping-key'
const KEY_ID = 'v1'

/** Holds the encrypted blobs, keyed by the same names localStorage used. */
const VAULT_STORE = 'vault'

const IV_BYTES = 12

type Sealed = {
  iv: Uint8Array<ArrayBuffer>
  data: ArrayBuffer
}

/**
 * Copies a stored buffer into this realm, or returns null if it is not one.
 *
 * `instanceof` is the obvious check and the wrong one. A structured clone
 * returns objects built by the storage implementation, which need not share
 * this realm's `Uint8Array` — the same mismatch that makes `instanceof` unsafe
 * across an iframe or a worker boundary. `ArrayBuffer.isView` and the
 * `toString` tag both read internal slots instead, so they answer the question
 * actually being asked: is this a buffer, wherever it was made.
 */
function toBytes(value: unknown): Uint8Array<ArrayBuffer> | null {
  if (ArrayBuffer.isView(value)) {
    return new Uint8Array(
      value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength),
    ) as Uint8Array<ArrayBuffer>
  }
  if (Object.prototype.toString.call(value) === '[object ArrayBuffer]') {
    return new Uint8Array((value as ArrayBuffer).slice(0)) as Uint8Array<ArrayBuffer>
  }
  return null
}

/** Unpacks a stored record, or null if it is not one this build wrote. */
function unseal(value: unknown): Sealed | null {
  if (typeof value !== 'object' || value === null) return null
  const candidate = value as { iv?: unknown; data?: unknown }
  const iv = toBytes(candidate.iv)
  const data = toBytes(candidate.data)
  if (!iv || !data || iv.byteLength !== IV_BYTES) return null
  return { iv, data: data.buffer }
}

/**
 * Whether this environment can hold a vault at all.
 *
 * Both halves are genuinely absent somewhere that matters: IndexedDB during
 * server rendering, `crypto.subtle` on any page served over plain HTTP. Callers
 * fall back to keeping keys in memory for the session rather than failing.
 */
export function vaultAvailable(): boolean {
  return (
    typeof indexedDB !== 'undefined' &&
    typeof crypto !== 'undefined' &&
    typeof crypto.subtle !== 'undefined'
  )
}

let dbPromise: Promise<IDBDatabase> | null = null

function openDatabase(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise
  const opening = new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION)
    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains(KEY_STORE)) db.createObjectStore(KEY_STORE)
      if (!db.objectStoreNames.contains(VAULT_STORE)) db.createObjectStore(VAULT_STORE)
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
    // Another tab holds an open connection at an older version. Rejecting is
    // right: the caller degrades to in-memory keys rather than hanging forever.
    request.onblocked = () => reject(new Error('indexeddb blocked'))
  })
  // A failed open must not stay cached, or one transient failure disables the
  // vault for the lifetime of the page.
  opening.catch(() => {
    if (dbPromise === opening) dbPromise = null
  })
  dbPromise = opening
  return opening
}

function transact<T>(
  store: string,
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  return openDatabase().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const tx = db.transaction(store, mode)
        const request = run(tx.objectStore(store))
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
        tx.onabort = () => reject(tx.error)
      }),
  )
}

let keyPromise: Promise<CryptoKey> | null = null

/**
 * The wrapping key, generated once per origin and never replaced.
 *
 * Never replaced on purpose: a new key makes every existing blob unreadable,
 * and no copy of the old one exists anywhere to recover them with. The one
 * situation that does replace it is the user clearing site data, which is also
 * the situation where losing the keys is what they asked for.
 */
function wrappingKey(): Promise<CryptoKey> {
  if (keyPromise) return keyPromise
  const deriving = (async () => {
    const existing = await transact<CryptoKey | undefined>(KEY_STORE, 'readonly', (store) =>
      store.get(KEY_ID),
    )
    if (existing) return existing

    const created = await crypto.subtle.generateKey(
      { name: 'AES-GCM', length: 256 },
      // The entire point. `extractable: false` is what stops script — ours or
      // anyone else's — reading the bytes back out and carrying them off.
      false,
      ['encrypt', 'decrypt'],
    )
    await transact(KEY_STORE, 'readwrite', (store) => store.put(created, KEY_ID))
    return created
  })()
  deriving.catch(() => {
    if (keyPromise === deriving) keyPromise = null
  })
  keyPromise = deriving
  return deriving
}

/**
 * Reads and decrypts a value, or returns [fallback].
 *
 * A blob that fails to decrypt reads as absent rather than throwing: the only
 * ways to get there are a truncated write or a tampered record, and neither is
 * something the caller can do anything about beyond starting fresh.
 */
export async function vaultRead<T>(key: string, fallback: T): Promise<T> {
  if (!vaultAvailable()) return fallback
  try {
    const sealed = unseal(
      await transact<unknown>(VAULT_STORE, 'readonly', (store) => store.get(key)),
    )
    if (!sealed) return fallback
    const plaintext = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: sealed.iv },
      await wrappingKey(),
      sealed.data,
    )
    return JSON.parse(new TextDecoder().decode(plaintext)) as T
  } catch {
    return fallback
  }
}

/** Encrypts and stores a value. Resolves `false` if it could not be persisted. */
export async function vaultWrite(key: string, value: unknown): Promise<boolean> {
  if (!vaultAvailable()) return false
  try {
    // A fresh IV per write, which AES-GCM requires: reusing one under the same
    // key is the failure that hands an attacker the keystream.
    const iv = crypto.getRandomValues(new Uint8Array(IV_BYTES))
    const data = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv },
      await wrappingKey(),
      new TextEncoder().encode(JSON.stringify(value)),
    )
    await transact(VAULT_STORE, 'readwrite', (store) =>
      store.put({ iv, data } satisfies Sealed, key),
    )
    return true
  } catch {
    return false
  }
}

export async function vaultRemove(key: string): Promise<void> {
  if (!vaultAvailable()) return
  try {
    await transact(VAULT_STORE, 'readwrite', (store) => store.delete(key))
  } catch {
    // Nothing useful to do; the value stays until the origin's data is cleared.
  }
}

/**
 * Empties the vault, keeping the wrapping key.
 *
 * Logout, and it deliberately does not destroy the key: another account may be
 * signing in on the same device a moment later, and generating a second key
 * would leave the first orphaned in the store forever.
 */
export async function vaultClear(): Promise<void> {
  if (!vaultAvailable()) return
  try {
    await transact(VAULT_STORE, 'readwrite', (store) => store.clear())
  } catch {
    // As above.
  }
}

/** Test seam: forgets the cached connection and key handle. */
export function resetVaultForTests(): void {
  dbPromise = null
  keyPromise = null
}
