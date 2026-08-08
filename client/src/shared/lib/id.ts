import { readStorage, StorageKeys, writeStorage } from './storage'

function randomId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/**
 * Stable per-installation device id.
 *
 * The gateway keys sessions, multi-device delivery and E2E key bundles on the
 * device id, and will mint one for us if we send none — but then every reload
 * would look like a brand-new device. Persisting it keeps one browser profile
 * as one device across reloads and reconnects.
 */
export function getDeviceId(): string {
  const stored = readStorage<string>(StorageKeys.deviceId, '')
  if (stored) return stored
  const created = `web-${randomId()}`
  writeStorage(StorageKeys.deviceId, created)
  return created
}

/**
 * Idempotency key for an outgoing message.
 *
 * The server enforces uniqueness on `(sender_id, dedup_key)`, so a retried send
 * after a reconnect resolves to the already-stored message (`duplicate: true`)
 * instead of posting twice. That guarantee is only as good as the key's
 * stability: it is generated once when the user hits send and reused for every
 * retry of that same message.
 */
export function createDedupKey(): string {
  return randomId()
}
