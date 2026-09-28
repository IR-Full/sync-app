'use client'

import { create } from 'zustand'

import { fromBase64, toBase64 } from '@/shared/lib/e2e/codec'
import {
  generateKeyPair,
  generateSigningKeyPair,
  type KeyPair,
  type SigningKeyPair,
} from '@/shared/lib/e2e/keys'
import type { SerializedSession } from '@/shared/lib/e2e/ratchet'
import { readStorage, removeStorage } from '@/shared/lib/storage'
import { vaultClear, vaultRead, vaultWrite } from '@/shared/lib/vault'

/**
 * How many one-time prekeys we publish per device.
 *
 * Was 8, which ran out after eight incoming first-messages — and a bundle with
 * no one-time prekey still completes X3DH, silently, using only the signed
 * prekey. So the ninth stranger to message this device got a session with
 * measurably weaker forward secrecy and nothing said so. 100 is the number
 * Signal's clients use and the directory caps at 256, so there is headroom.
 */
export const ONE_TIME_PREKEY_COUNT = 100

/**
 * Top back up once the pool falls below this.
 *
 * Refilling on every consumption would republish the whole bundle for each
 * incoming first-message; refilling only at zero means the pool is empty for as
 * long as it takes to notice. A quarter is the usual compromise.
 */
export const ONE_TIME_PREKEY_LOW_WATER = Math.floor(ONE_TIME_PREKEY_COUNT / 4)

/**
 * How long a signed prekey is used before a fresh one replaces it.
 *
 * The signed prekey is the medium-term key: it is what an initiator does DH
 * against when starting a session, so a device that never rotates it has one key
 * standing behind every session it will ever receive, forever. Compromising it
 * long after the fact opens the initial handshake of every conversation started
 * since. Seven days is the interval Signal documents.
 */
export const SIGNED_PREKEY_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000

/**
 * Ceiling on one-time prekeys kept LOCALLY, mirroring the directory's own cap.
 *
 * The pool has to be able to grow past ONE_TIME_PREKEY_COUNT: when the directory
 * reports it is running low, the keys it is missing are ones it already served, so
 * topping it up means minting NEW pairs while the old private halves are still
 * needed for first-messages in flight. Unbounded, that grows by a batch every time
 * the device gets popular. Over the cap the OLDEST are dropped, which is what the
 * directory does too - so the key dropped here is the one it dropped there, or the
 * one that has gone longest without being claimed.
 */
export const MAX_LOCAL_PREKEYS = 256

/**
 * What the directory reports back on KEY_PUBLISH.
 *
 * It is not a nicety. One-time prekeys are consumed by PEERS fetching bundles, so
 * this device cannot watch its own balance fall: the local pool only shrinks when a
 * message actually decrypts with a key, which misses every fetch that never became a
 * message - a peer that gave up, a bundle fetched for a multi-device send where
 * another device answered. Left to the local count, the directory empties while this
 * device believes it is full, and every session started after that silently uses
 * three Diffie-Hellmans instead of four.
 */
export interface DirectoryKeyState {
  /** prekeys the directory holds AFTER the publish that reported this */
  oneTimePrekeysLeft: number
  /** age of the signed prekey the DIRECTORY holds; 0 when it has just been set */
  signedPrekeyAgeMs: number
  /** how many prekeys from the reporting frame the directory kept */
  accepted: number
}

export interface SecretIdentity {
  identity: KeyPair
  signing: SigningKeyPair
  signedPreKey: KeyPair
  /** when `signedPreKey` was minted, in unix millis — drives rotation */
  signedPreKeyCreatedAt: number
  /**
   * The signed prekey this device used BEFORE the last rotation.
   *
   * Kept for one generation because rotation is not atomic across the network:
   * a peer may have fetched the old bundle seconds before the rotation and be
   * about to send its first message against it. Without a grace copy that
   * message is undecryptable, and the sender is never told — it is fire and
   * forget. One generation plus a weekly rotation means a bundle has to be a
   * week stale before it fails.
   */
  previousSignedPreKey?: KeyPair
  /** unconsumed one-time prekeys, kept so we can complete X3DH as responder */
  oneTimePreKeys: KeyPair[]
  /**
   * Base64 public halves the directory has CONFIRMED it stored.
   *
   * Publishing is additive on the server: it appends, it does not replace. So
   * republishing the whole pool on every connect - which is what this did - filed the
   * same public key two, three, ten times over, and a one-time prekey handed out
   * twice is a one-time prekey used twice, which is the one thing it exists not to be.
   * Tracking what the directory acknowledged means each key is offered once.
   */
  publishedPreKeys: string[]
}

export interface SecretMessage {
  id: string
  /** peer user id */
  peerId: string
  text: string
  timestamp: number
  outgoing: boolean
  /** set when decryption failed, so the failure is visible rather than silent */
  failed?: boolean
}

interface StoredIdentity {
  identity: { privateKey: string; publicKey: string }
  signing: { privateKey: string; publicKey: string }
  signedPreKey: { privateKey: string; publicKey: string }
  signedPreKeyCreatedAt?: number
  previousSignedPreKey?: { privateKey: string; publicKey: string }
  oneTimePreKeys: { privateKey: string; publicKey: string }[]
  publishedPreKeys?: string[]
}

interface SecretState {
  ownerId: string | null
  identity: SecretIdentity | null
  /** "userId:deviceId" -> serialised ratchet state */
  sessions: Record<string, SerializedSession>
  /** peer user id -> local transcript (the server stores nothing) */
  transcripts: Record<string, SecretMessage[]>
  published: boolean

  /** true once [hydrate] has finished for the current owner */
  hydrated: boolean

  /**
   * Loads this account's keys, creating them on first use.
   *
   * Asynchronous because the vault is: IndexedDB and `crypto.subtle` are both
   * promise-based, and there is no synchronous way to read an encrypted blob.
   * Idempotent and safe to call concurrently — the in-flight promise is shared,
   * so two callers cannot race into generating two identities.
   */
  hydrate: (ownerId: string) => Promise<SecretIdentity>
  /**
   * Rotates the signed prekey if it is older than SIGNED_PREKEY_MAX_AGE_MS and
   * refills the one-time pool if it has run low. Returns true when anything
   * changed, so the caller knows to republish.
   */
  maintainKeys: () => boolean
  /**
   * Folds the directory's own report of this device's bundle back into local state.
   *
   * `sent` is the prekey list of the publish being answered, in the order it went out,
   * so the keys the directory kept can be marked as offered. Returns true when the
   * bundle has to go out again - because the directory is running low and fresh keys
   * were minted for it, or because what it holds is older than the rotation window and
   * this device's last publish evidently never landed.
   */
  applyDirectoryState: (state: DirectoryKeyState, sent: string[]) => boolean
  /** Prekeys the directory has not confirmed yet - what a publish should carry. */
  unpublishedPreKeys: () => KeyPair[]
  setPublished: (published: boolean) => void
  saveSession: (key: string, session: SerializedSession) => void
  dropOneTimePreKey: (publicKey: string) => void
  appendMessage: (message: SecretMessage) => void
  /** Forgets the loaded account, leaving its keys on disk. What a reload does. */
  reset: () => void
  /** Forgets the loaded account AND erases its keys. What a logout does. */
  forget: () => void
}

const encodePair = (pair: KeyPair | SigningKeyPair) => ({
  privateKey: toBase64(pair.privateKey),
  publicKey: toBase64(pair.publicKey),
})

const decodePair = (pair: { privateKey: string; publicKey: string }): KeyPair => ({
  privateKey: fromBase64(pair.privateKey),
  publicKey: fromBase64(pair.publicKey),
})

function identityKey(ownerId: string) {
  return `SyncApp:e2e-identity:${ownerId}`
}
function sessionsKey(ownerId: string) {
  return `SyncApp:e2e-sessions:${ownerId}`
}
function transcriptsKey(ownerId: string) {
  return `SyncApp:e2e-transcripts:${ownerId}`
}

/**
 * Writes to the vault without making every caller async.
 *
 * The store's writers are called from click handlers and from the middle of a
 * ratchet step, and neither can usefully wait on a disk write or do anything
 * about one that fails. The in-memory state is the truth for this session; the
 * vault is how it survives a reload. A dropped write costs a re-derived
 * identity or a forgotten session on the next visit, not a wrong answer now.
 */
let writes: Promise<unknown> = Promise.resolve()

function persist(key: string, value: unknown): void {
  // Queued rather than fired in parallel. Two writes to the same key are two
  // states of the same ratchet, and letting the older one land second would
  // persist a session that has already advanced.
  writes = writes.then(() => vaultWrite(key, value)).catch(() => undefined)
}

/**
 * Resolves once every queued write has landed.
 *
 * Exists for tests, which have to observe a write the app deliberately does not
 * wait for. Cheap enough to be honest about: it is the same promise the writes
 * are already chained on.
 */
export function whenPersisted(): Promise<void> {
  return writes.then(() => undefined)
}

/**
 * Moves a value written by an older build out of localStorage.
 *
 * Every installation before the vault kept its private keys as a JSON string
 * that any script in this origin could read. Copying them across and deleting
 * the original is the whole migration; it runs once, on the first hydrate after
 * the update, and does nothing on a device that never had them.
 */
async function migrateFromLocalStorage<T>(key: string, fallback: T): Promise<T | null> {
  const legacy = readStorage<T | null>(key, null)
  if (legacy === null) return null
  const moved = await vaultWrite(key, legacy)
  // Only after the vault has it. Removing first would lose the keys outright
  // on a browser where the vault is unavailable.
  if (moved) removeStorage(key)
  return legacy ?? fallback
}

function createIdentity(): SecretIdentity {
  return {
    identity: generateKeyPair(),
    signing: generateSigningKeyPair(),
    signedPreKey: generateKeyPair(),
    signedPreKeyCreatedAt: Date.now(),
    oneTimePreKeys: Array.from({ length: ONE_TIME_PREKEY_COUNT }, () => generateKeyPair()),
    publishedPreKeys: [],
  }
}

/** Serialises an identity for storage. Used by every writer, so the shape cannot
 * drift between the three places that persist it. */
function encodeIdentity(identity: SecretIdentity): StoredIdentity {
  return {
    identity: encodePair(identity.identity),
    signing: encodePair(identity.signing),
    signedPreKey: encodePair(identity.signedPreKey),
    signedPreKeyCreatedAt: identity.signedPreKeyCreatedAt,
    previousSignedPreKey: identity.previousSignedPreKey
      ? encodePair(identity.previousSignedPreKey)
      : undefined,
    oneTimePreKeys: identity.oneTimePreKeys.map(encodePair),
    publishedPreKeys: identity.publishedPreKeys,
  }
}

/**
 * Long-term secret-chat material for this device.
 *
 * **Private keys are AES-GCM ciphertext in IndexedDB**, under a wrapping key
 * the browser generated as non-extractable — see `shared/lib/vault`. The
 * ratchet needs raw key bytes and ChaCha20-Poly1305, so the keys themselves
 * cannot be non-extractable WebCrypto handles; wrapping them is the nearest
 * thing the platform offers. It does not stop script that is already running in
 * this origin, which can call the vault as we do. It does stop the keys being
 * read out of storage as a string and carried off, which is what the ordinary
 * XSS payload does and what `localStorage` made trivial.
 *
 * Transcripts are local and wrapped the same way: the server relays secret
 * messages and stores nothing, so history exists only on the devices that
 * received it.
 */
export const useSecretStore = create<SecretState>((set, get) => ({
  ownerId: null,
  identity: null,
  sessions: {},
  transcripts: {},
  published: false,

  hydrated: false,

  hydrate: (ownerId) => {
    const state = get()
    // Already loaded for this account. The owner check is the part that
    // matters: the store outlives a logout, and without it a second account
    // signing in on the same browser would publish the first one's identity
    // key as its own.
    if (state.hydrated && state.ownerId === ownerId && state.identity) {
      return Promise.resolve(state.identity)
    }
    if (inFlight?.ownerId === ownerId) return inFlight.promise

    const promise = hydrateFrom(ownerId, set)
    inFlight = { ownerId, promise }
    void promise.finally(() => {
      if (inFlight?.promise === promise) inFlight = null
    })
    return promise
  },
  maintainKeys: () => {
    const { ownerId, identity } = get()
    if (!ownerId || !identity) return false

    let next = identity
    let changed = false

    if (Date.now() - identity.signedPreKeyCreatedAt >= SIGNED_PREKEY_MAX_AGE_MS) {
      next = {
        ...next,
        // The outgoing key becomes the grace copy; the one it replaces is
        // dropped, which is the point — a key kept forever is a key that never
        // rotated.
        previousSignedPreKey: next.signedPreKey,
        signedPreKey: generateKeyPair(),
        signedPreKeyCreatedAt: Date.now(),
      }
      changed = true
    }

    if (next.oneTimePreKeys.length <= ONE_TIME_PREKEY_LOW_WATER) {
      const missing = ONE_TIME_PREKEY_COUNT - next.oneTimePreKeys.length
      next = {
        ...next,
        oneTimePreKeys: [
          ...next.oneTimePreKeys,
          ...Array.from({ length: missing }, () => generateKeyPair()),
        ],
      }
      changed = true
    }

    if (!changed) return false
    persist(identityKey(ownerId), encodeIdentity(next))
    set({ identity: next, published: false })
    return true
  },

  applyDirectoryState: (state, sent) => {
    const { ownerId, identity } = get()
    if (!ownerId || !identity) return false

    // The survivors of a truncated frame are its LAST `accepted` keys: the
    // directory appends and trims from the front, so the newest are the ones that
    // stay. Marking the first `accepted` instead would retire keys the directory
    // dropped and offer the stored ones again, which is the duplicate this tracking
    // exists to prevent, in the one case where it is guaranteed to happen.
    const kept = state.accepted >= sent.length ? sent : sent.slice(sent.length - state.accepted)
    let next: SecretIdentity = {
      ...identity,
      publishedPreKeys: [...new Set([...identity.publishedPreKeys, ...kept])],
    }

    // Republishing is driven by what the DIRECTORY reports, never by what is still
    // unpublished here. The difference matters: an unpublished remainder means the
    // per-publish cap truncated the frame, and republishing on that would send the
    // remainder, be truncated again, and loop forever against a full directory.
    let republish = false

    if (state.oneTimePrekeysLeft <= ONE_TIME_PREKEY_LOW_WATER) {
      const missing = ONE_TIME_PREKEY_COUNT - state.oneTimePrekeysLeft
      // Fresh pairs, not the ones already in the pool. The keys the directory is
      // missing are precisely the ones it has already handed out.
      const minted = Array.from({ length: missing }, () => generateKeyPair())
      next = { ...next, oneTimePreKeys: [...next.oneTimePreKeys, ...minted] }
      republish = true
    }

    if (next.oneTimePreKeys.length > MAX_LOCAL_PREKEYS) {
      const dropped = next.oneTimePreKeys.slice(
        0,
        next.oneTimePreKeys.length - MAX_LOCAL_PREKEYS,
      )
      const goneKeys = new Set(dropped.map((pair) => toBase64(pair.publicKey)))
      next = {
        ...next,
        oneTimePreKeys: next.oneTimePreKeys.slice(
          next.oneTimePreKeys.length - MAX_LOCAL_PREKEYS,
        ),
        // The bookkeeping has to shrink with the pool, or it grows without bound on
        // its own - a list of base64 strings for every key this device ever offered.
        publishedPreKeys: next.publishedPreKeys.filter((key) => !goneKeys.has(key)),
      }
    }

    // The directory is holding a signed prekey older than the rotation window while
    // this device may well believe it rotated. Only a republish can settle it: if the
    // local key is genuinely that old, maintainKeys rotates on the next pass and the
    // new one goes out; if it is fresh, the last publish never landed and this sends
    // it again. Either way the directory's next report reads 0.
    if (state.signedPrekeyAgeMs >= SIGNED_PREKEY_MAX_AGE_MS) republish = true

    persist(identityKey(ownerId), encodeIdentity(next))
    set({ identity: next, published: !republish })
    return republish
  },

  unpublishedPreKeys: () => {
    const identity = get().identity
    if (!identity) return []
    const published = new Set(identity.publishedPreKeys)
    return identity.oneTimePreKeys.filter((pair) => !published.has(toBase64(pair.publicKey)))
  },

  setPublished: (published) => set({ published }),

  saveSession: (key, session) => {
    const { ownerId, sessions } = get()
    const next = { ...sessions, [key]: session }
    if (ownerId) persist(sessionsKey(ownerId), next)
    set({ sessions: next })
  },

  /**
   * Forgets a one-time prekey once it has been used.
   *
   * The protocol never tells the publisher which prekey a fetcher consumed, so
   * "used" is only discovered when a message actually decrypts with it. Dropping
   * it then keeps the candidate list short and stops it being reused.
   */
  dropOneTimePreKey: (publicKey) => {
    const { ownerId, identity } = get()
    if (!identity || !ownerId) return
    const remaining = identity.oneTimePreKeys.filter(
      (pair) => toBase64(pair.publicKey) !== publicKey,
    )
    if (remaining.length === identity.oneTimePreKeys.length) return
    const next = { ...identity, oneTimePreKeys: remaining }
    persist(identityKey(ownerId), encodeIdentity(next))
    set({ identity: next })
  },

  appendMessage: (message) => {
    const { ownerId, transcripts } = get()
    const forPeer = [...(transcripts[message.peerId] ?? []), message]
    const next = { ...transcripts, [message.peerId]: forPeer }
    if (ownerId) persist(transcriptsKey(ownerId), next)
    set({ transcripts: next })
  },

  /**
   * Drops the loaded account from memory, leaving the vault alone.
   *
   * What a reload does. The keys are still on disk and the next [hydrate] finds
   * them, which is the point — a device that minted a new identity on every
   * reload would invalidate every peer's session without saying so.
   */
  reset: () => {
    inFlight = null
    set({
      ownerId: null,
      identity: null,
      sessions: {},
      transcripts: {},
      published: false,
      hydrated: false,
    })
  },

  /**
   * Drops the account from memory AND erases the vault.
   *
   * Logout. Keeping the keys would be defensible — it spares every peer an
   * identity-changed warning on the next login — but it means a shared machine
   * holds a stranger's private keys and their secret transcripts after they
   * believe they have left, and that is the worse failure. The cost is real and
   * is paid by the peer, who is asked to compare a safety number again; the
   * warning is accurate, because from their side the device key genuinely did
   * change.
   */
  forget: () => {
    get().reset()
    // Queued behind the pending writes, so a save still in flight cannot land
    // after the clear and resurrect what was just erased.
    writes = writes.then(() => vaultClear()).catch(() => undefined)
  },
}))

/**
 * Reads this account's keys out of the vault, creating them if there are none.
 *
 * Split out of the store so the store literal stays a list of operations rather
 * than one long function, and so the in-flight deduplication in [hydrate] has
 * something to hold.
 */
async function hydrateFrom(
  ownerId: string,
  set: (partial: Partial<SecretState>) => void,
): Promise<SecretIdentity> {
  // Any write still queued belongs to the state being read. Reading past it
  // would load a session the ratchet has already advanced beyond, or — right
  // after a logout — resurrect keys the clear had not yet reached.
  await whenPersisted()

  const idKey = identityKey(ownerId)
  const stored =
    (await vaultRead<StoredIdentity | null>(idKey, null)) ??
    (await migrateFromLocalStorage<StoredIdentity>(idKey, null as never))

  let identity: SecretIdentity
  if (stored) {
    identity = {
      identity: decodePair(stored.identity),
      signing: decodePair(stored.signing),
      signedPreKey: decodePair(stored.signedPreKey),
      // An identity stored before rotation existed has no timestamp. Treating
      // it as "created now" rather than "created at the epoch" is deliberate:
      // the alternative rotates every such device on its next launch, which is
      // a thundering herd of republishes for a key that is not known to be old.
      signedPreKeyCreatedAt: stored.signedPreKeyCreatedAt ?? Date.now(),
      previousSignedPreKey: stored.previousSignedPreKey
        ? decodePair(stored.previousSignedPreKey)
        : undefined,
      oneTimePreKeys: (stored.oneTimePreKeys ?? []).map(decodePair),
      // An identity stored before this was tracked has published everything it
      // holds, as far as anyone can tell - but saying so would suppress the one
      // publish that teaches us otherwise. Empty means "offer them again": one
      // last duplicated batch on this device, against a directory that caps and
      // trims, in exchange for exact accounting from then on.
      publishedPreKeys: stored.publishedPreKeys ?? [],
    }
  } else {
    identity = createIdentity()
  }
  persist(idKey, encodeIdentity(identity))

  const sessionsAt = sessionsKey(ownerId)
  const transcriptsAt = transcriptsKey(ownerId)
  const [sessions, transcripts] = await Promise.all([
    vaultRead<Record<string, SerializedSession> | null>(sessionsAt, null).then(
      async (found) =>
        found ??
        (await migrateFromLocalStorage<Record<string, SerializedSession>>(sessionsAt, {})) ??
        {},
    ),
    vaultRead<Record<string, SecretMessage[]> | null>(transcriptsAt, null).then(
      async (found) =>
        found ??
        (await migrateFromLocalStorage<Record<string, SecretMessage[]>>(transcriptsAt, {})) ??
        {},
    ),
  ])

  set({ ownerId, identity, sessions, transcripts, published: false, hydrated: true })
  return identity
}

/** Deduplicates concurrent [hydrate] calls; see the comment there. */
let inFlight: { ownerId: string; promise: Promise<SecretIdentity> } | null = null

export function useSecretTranscript(peerId: string): SecretMessage[] {
  return useSecretStore((state) => state.transcripts[peerId] ?? EMPTY)
}

const EMPTY: SecretMessage[] = []
