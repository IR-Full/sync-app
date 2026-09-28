import { beforeEach, describe, expect, it, vi } from 'vitest'

import { toBase64 } from '@/shared/lib/e2e/codec'
import { generateKeyPair } from '@/shared/lib/e2e/keys'
import { vaultRead, vaultWrite } from '@/shared/lib/vault'
import {
  MAX_LOCAL_PREKEYS,
  ONE_TIME_PREKEY_COUNT,
  SIGNED_PREKEY_MAX_AGE_MS,
  useSecretStore,
  whenPersisted,
  type DirectoryKeyState,
  type SecretMessage,
} from './model'

const store = () => useSecretStore.getState()

/**
 * Reads what was actually written to disk.
 *
 * The store's writes are deliberately not awaited by the app — a click handler
 * has nothing useful to do while a key is being encrypted — so a test that
 * wants to see one has to flush the queue first.
 */
async function stored<T>(key: string): Promise<T | null> {
  await whenPersisted()
  return vaultRead<T | null>(key, null)
}

const storedIdentity = (ownerId: string) =>
  stored<Record<string, unknown>>(`SyncApp:e2e-identity:${ownerId}`)

const message = (overrides: Partial<SecretMessage> = {}): SecretMessage => ({
  id: 'sm1',
  peerId: 'u2',
  text: 'hello',
  timestamp: 1_000,
  outgoing: true,
  ...overrides,
})

beforeEach(async () => {
  localStorage.clear()
  vi.restoreAllMocks()
  store().forget()
  await whenPersisted()
})

describe('migration out of localStorage', () => {
  /**
   * Every installation before the vault kept its private keys as a JSON string
   * in localStorage, where any script in this origin could read them. The
   * migration is the only thing that gets those devices out; without it they
   * would keep their keys exactly where they were, and the change would protect
   * nobody who was already using the app.
   */
  it('adopts an identity written by an older build', async () => {
    const minted = await store().hydrate('u1')
    const legacy = await stored<Record<string, unknown>>('SyncApp:e2e-identity:u1')
    store().forget()
    localStorage.setItem('SyncApp:e2e-identity:u1', JSON.stringify(legacy))

    const adopted = await store().hydrate('u1')

    expect(toBase64(adopted.identity.privateKey)).toBe(toBase64(minted.identity.privateKey))
  })

  it('moves it into the vault', async () => {
    await store().hydrate('u1')
    const legacy = await stored<Record<string, unknown>>('SyncApp:e2e-identity:u1')
    store().forget()
    localStorage.setItem('SyncApp:e2e-identity:u1', JSON.stringify(legacy))

    await store().hydrate('u1')

    expect(await stored('SyncApp:e2e-identity:u1')).toEqual(legacy)
  })

  /**
   * The point of moving them. A key left behind in localStorage is still
   * readable by the one-line payload the move exists to defeat.
   */
  it('deletes the localStorage copy', async () => {
    await store().hydrate('u1')
    const legacy = await stored<Record<string, unknown>>('SyncApp:e2e-identity:u1')
    store().forget()
    localStorage.setItem('SyncApp:e2e-identity:u1', JSON.stringify(legacy))

    await store().hydrate('u1')

    expect(localStorage.getItem('SyncApp:e2e-identity:u1')).toBeNull()
  })

  it('carries sessions and transcripts across too', async () => {
    localStorage.setItem(
      'SyncApp:e2e-sessions:u1',
      JSON.stringify({ 'u2:d2': { rootKey: 'rk' } }),
    )
    localStorage.setItem('SyncApp:e2e-transcripts:u1', JSON.stringify({ u2: [message()] }))

    await store().hydrate('u1')

    expect(store().sessions['u2:d2']).toMatchObject({ rootKey: 'rk' })
    expect(store().transcripts.u2).toHaveLength(1)
    expect(localStorage.getItem('SyncApp:e2e-sessions:u1')).toBeNull()
  })

  it('prefers the vault once the move has happened', async () => {
    await store().hydrate('u1')
    store().saveSession('u2:d2', { rootKey: 'rk' } as never)
    await whenPersisted()
    store().reset()
    // A stale localStorage copy left by a half-finished migration must not
    // override the keys already in the vault.
    localStorage.setItem(
      'SyncApp:e2e-sessions:u1',
      JSON.stringify({ 'u9:d9': { rootKey: 'x' } }),
    )

    await store().hydrate('u1')

    expect(store().sessions['u9:d9']).toBeUndefined()
    expect(store().sessions['u2:d2']).toMatchObject({ rootKey: 'rk' })
  })
})

describe('hydrate', () => {
  it('mints an identity on first use', async () => {
    const identity = await store().hydrate('u1')

    expect(identity.identity.privateKey.length).toBe(32)
    expect(identity.signing.privateKey.length).toBe(32)
    expect(identity.signedPreKey.privateKey.length).toBe(32)
  })

  it('publishes the configured number of one-time prekeys', async () => {
    expect((await store().hydrate('u1')).oneTimePreKeys).toHaveLength(ONE_TIME_PREKEY_COUNT)
  })

  it('mints distinct one-time prekeys', async () => {
    // Two identical prekeys would let one X3DH handshake open two sessions.
    const keys = (await store().hydrate('u1')).oneTimePreKeys.map((pair) =>
      toBase64(pair.publicKey),
    )
    expect(new Set(keys).size).toBe(keys.length)
  })

  /**
   * The identity is the device's long-term secret-chat key: it is what the peer
   * verified when the session began. Minting a fresh one on every load would
   * silently invalidate every existing ratchet session and every published
   * bundle, and the symptom would be undecryptable messages, not an error.
   */
  it('reuses a stored identity across reloads', async () => {
    const first = await store().hydrate('u1')
    store().reset()
    const second = await store().hydrate('u1')

    expect(toBase64(second.identity.privateKey)).toBe(toBase64(first.identity.privateKey))
    expect(toBase64(second.signedPreKey.publicKey)).toBe(toBase64(first.signedPreKey.publicKey))
  })

  it('round-trips the one-time prekeys through storage intact', async () => {
    const first = await store().hydrate('u1')
    store().reset()
    const second = await store().hydrate('u1')

    expect(second.oneTimePreKeys.map((pair) => toBase64(pair.privateKey))).toEqual(
      first.oneTimePreKeys.map((pair) => toBase64(pair.privateKey)),
    )
  })

  it('gives each account its own identity', async () => {
    // Two accounts in one browser must not share a device key.
    const first = await store().hydrate('u1')
    const second = await store().hydrate('u2')

    expect(toBase64(second.identity.privateKey)).not.toBe(toBase64(first.identity.privateKey))
  })

  it('persists the identity as base64, not as raw byte arrays', async () => {
    // A Uint8Array JSON-serialises to `{"0":12,...}`, which decodes back to the
    // wrong thing entirely — silently producing a key that never agrees.
    await store().hydrate('u1')
    const identity = (await storedIdentity('u1')) as { identity: { privateKey: string } }
    expect(typeof identity.identity.privateKey).toBe('string')
  })

  it('tolerates a stored identity that predates one-time prekeys', async () => {
    await store().hydrate('u1')
    const identity = (await storedIdentity('u1'))!
    delete identity.oneTimePreKeys
    await vaultWrite('SyncApp:e2e-identity:u1', identity)

    store().reset()
    expect((await store().hydrate('u1')).oneTimePreKeys).toEqual([])
  })

  it('restores stored sessions and transcripts', async () => {
    await store().hydrate('u1')
    store().saveSession('u2:d2', { rootKey: 'rk' } as never)
    store().appendMessage(message())

    store().reset()
    await store().hydrate('u1')

    expect(store().sessions['u2:d2']).toMatchObject({ rootKey: 'rk' })
    expect(store().transcripts.u2).toHaveLength(1)
  })

  it('starts unpublished, so a reconnect re-publishes the bundle', async () => {
    // The gateway keeps prekeys per session; a client that assumed it was still
    // published would leave peers unable to start a chat.
    await store().hydrate('u1')
    store().setPublished(true)
    store().reset()
    await store().hydrate('u1')

    expect(store().published).toBe(false)
  })

  it('records the owner so later writes land under the right key', async () => {
    await store().hydrate('u1')
    expect(store().ownerId).toBe('u1')
  })
})

describe('saveSession', () => {
  it('stores a session by peer key', async () => {
    await store().hydrate('u1')
    store().saveSession('u2:d2', { rootKey: 'rk' } as never)
    expect(store().sessions['u2:d2']).toMatchObject({ rootKey: 'rk' })
  })

  it('persists it', async () => {
    // Losing a ratchet session means the peer's next message cannot be opened
    // and the chat has to be restarted from scratch.
    await store().hydrate('u1')
    store().saveSession('u2:d2', { rootKey: 'rk' } as never)

    expect(
      (await stored<Record<string, unknown>>('SyncApp:e2e-sessions:u1'))!['u2:d2'],
    ).toMatchObject({ rootKey: 'rk' })
  })

  it('keeps one peer`s devices in separate sessions', async () => {
    // A ratchet session is per device, not per user: one account on two devices
    // runs two independent ratchets.
    await store().hydrate('u1')
    store().saveSession('u2:phone', { rootKey: 'a' } as never)
    store().saveSession('u2:laptop', { rootKey: 'b' } as never)

    expect(Object.keys(store().sessions).sort()).toEqual(['u2:laptop', 'u2:phone'])
  })

  it('overwrites the session as the ratchet advances', async () => {
    await store().hydrate('u1')
    store().saveSession('u2:d2', { rootKey: 'old', sent: 1 } as never)
    store().saveSession('u2:d2', { rootKey: 'new', sent: 2 } as never)

    expect(store().sessions['u2:d2']).toMatchObject({ rootKey: 'new', sent: 2 })
  })

  it('does not persist when no owner is loaded', async () => {
    store().saveSession('u2:d2', { rootKey: 'rk' } as never)
    expect(await stored('SyncApp:e2e-sessions:u1')).toBeNull()
  })
})

describe('dropOneTimePreKey', () => {
  /**
   * The key directory hands a fetcher one of our prekeys and never tells us
   * which, so "used" is only discovered when a message decrypts with it. Keeping
   * a consumed key would let it be reused, which defeats the forward secrecy the
   * one-time key exists to provide.
   */
  it('removes the consumed key', async () => {
    const identity = await store().hydrate('u1')
    const consumed = toBase64(identity.oneTimePreKeys[0].publicKey)

    store().dropOneTimePreKey(consumed)

    const remaining = store().identity!.oneTimePreKeys.map((pair) => toBase64(pair.publicKey))
    expect(remaining).toHaveLength(ONE_TIME_PREKEY_COUNT - 1)
    expect(remaining).not.toContain(consumed)
  })

  it('persists the removal', async () => {
    const identity = await store().hydrate('u1')
    store().dropOneTimePreKey(toBase64(identity.oneTimePreKeys[0].publicKey))

    store().reset()
    expect((await store().hydrate('u1')).oneTimePreKeys).toHaveLength(ONE_TIME_PREKEY_COUNT - 1)
  })

  it('leaves the long-term keys untouched', async () => {
    const identity = await store().hydrate('u1')
    const before = toBase64(identity.identity.privateKey)
    store().dropOneTimePreKey(toBase64(identity.oneTimePreKeys[0].publicKey))

    expect(toBase64(store().identity!.identity.privateKey)).toBe(before)
  })

  it('does nothing for a key we never held', async () => {
    await store().hydrate('u1')
    store().dropOneTimePreKey('bm90LWEta2V5')
    expect(store().identity!.oneTimePreKeys).toHaveLength(ONE_TIME_PREKEY_COUNT)
  })

  it('is idempotent', async () => {
    // The same first message can be delivered twice after a reconnect.
    const identity = await store().hydrate('u1')
    const consumed = toBase64(identity.oneTimePreKeys[0].publicKey)
    store().dropOneTimePreKey(consumed)
    store().dropOneTimePreKey(consumed)

    expect(store().identity!.oneTimePreKeys).toHaveLength(ONE_TIME_PREKEY_COUNT - 1)
  })

  it('does nothing when no identity is loaded', async () => {
    expect(() => store().dropOneTimePreKey('anything')).not.toThrow()
  })

  it('can exhaust the pool without error', async () => {
    // A popular device runs out; X3DH still works without a one-time prekey.
    const identity = await store().hydrate('u1')
    for (const pair of [...identity.oneTimePreKeys]) {
      store().dropOneTimePreKey(toBase64(pair.publicKey))
    }
    expect(store().identity!.oneTimePreKeys).toEqual([])
  })
})

describe('appendMessage', () => {
  it('appends to the peer transcript', async () => {
    await store().hydrate('u1')
    store().appendMessage(message({ id: 'a' }))
    store().appendMessage(message({ id: 'b' }))

    expect(store().transcripts.u2.map((entry) => entry.id)).toEqual(['a', 'b'])
  })

  /**
   * The server relays secret messages and stores nothing, so this transcript is
   * the only copy that exists on this device. A write that is not persisted is a
   * conversation lost on reload.
   */
  it('persists the transcript', async () => {
    await store().hydrate('u1')
    store().appendMessage(message())

    expect(
      (await stored<Record<string, SecretMessage[]>>('SyncApp:e2e-transcripts:u1'))!.u2,
    ).toHaveLength(1)
  })

  it('keeps peers separate', async () => {
    await store().hydrate('u1')
    store().appendMessage(message({ peerId: 'u2' }))
    store().appendMessage(message({ peerId: 'u3' }))

    expect(Object.keys(store().transcripts).sort()).toEqual(['u2', 'u3'])
  })

  it('records a failed decryption rather than dropping the message', async () => {
    // A silently missing message is indistinguishable from one that was never
    // sent; a visible failure at least tells the user to ask for a resend.
    await store().hydrate('u1')
    store().appendMessage(message({ failed: true, text: '' }))

    expect(store().transcripts.u2[0].failed).toBe(true)
  })

  it('keeps direction, which is what decides the bubble side', async () => {
    await store().hydrate('u1')
    store().appendMessage(message({ id: 'out', outgoing: true }))
    store().appendMessage(message({ id: 'in', outgoing: false }))

    expect(store().transcripts.u2.map((entry) => entry.outgoing)).toEqual([true, false])
  })

  it('does not persist when no owner is loaded', async () => {
    store().appendMessage(message())
    expect(await stored('SyncApp:e2e-transcripts:u1')).toBeNull()
  })
})

describe('reset', () => {
  it('clears the in-memory state', async () => {
    await store().hydrate('u1')
    store().appendMessage(message())
    store().reset()

    expect(store()).toMatchObject({
      ownerId: null,
      identity: null,
      sessions: {},
      transcripts: {},
      published: false,
    })
  })

  it('leaves the stored identity, so a reload keeps the device key', async () => {
    // Discarding it here would invalidate every peer's existing session on
    // something as ordinary as a refresh.
    await store().hydrate('u1')
    const before = await storedIdentity('u1')
    store().reset()

    expect(await storedIdentity('u1')).toEqual(before)
  })
})

describe('forget', () => {
  /**
   * Logout, and the reason the vault is erased rather than kept: a shared
   * machine must not hold a stranger's private keys and secret transcripts
   * after they believe they have left.
   */
  it('erases the stored identity', async () => {
    await store().hydrate('u1')
    store().forget()

    expect(await storedIdentity('u1')).toBeNull()
  })

  it('erases stored sessions and transcripts', async () => {
    await store().hydrate('u1')
    store().saveSession('u2:d2', { rootKey: 'rk' } as never)
    store().appendMessage(message())
    store().forget()

    expect(await stored('SyncApp:e2e-sessions:u1')).toBeNull()
    expect(await stored('SyncApp:e2e-transcripts:u1')).toBeNull()
  })

  it('mints a new identity on the next hydrate', async () => {
    const before = await store().hydrate('u1')
    store().forget()
    const after = await store().hydrate('u1')

    expect(toBase64(after.identity.privateKey)).not.toBe(toBase64(before.identity.privateKey))
  })

  /**
   * A save still in flight when the user logs out must not land afterwards and
   * put the keys back.
   */
  it('outlasts a write that was still in flight', async () => {
    await store().hydrate('u1')
    store().saveSession('u2:d2', { rootKey: 'rk' } as never)
    store().forget()

    expect(await stored('SyncApp:e2e-sessions:u1')).toBeNull()
  })
})

describe('setPublished', () => {
  it('records that the bundle reached the key directory', async () => {
    await store().hydrate('u1')
    store().setPublished(true)
    expect(store().published).toBe(true)
  })
})

describe('prekey maintenance', () => {
  beforeEach(async () => {
    localStorage.clear()
    useSecretStore.getState().forget()
    await whenPersisted()
  })

  /**
   * The pool used to hold 8 keys and never refill. A bundle with no one-time
   * prekey still completes X3DH — silently, on the signed prekey alone — so the
   * ninth stranger to message this device got a measurably weaker session and
   * nothing said so.
   */
  it('mints a full pool of one-time prekeys', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    expect(identity.oneTimePreKeys).toHaveLength(ONE_TIME_PREKEY_COUNT)
  })

  it('does nothing while the keys are fresh', async () => {
    await useSecretStore.getState().hydrate('u1')
    useSecretStore.getState().setPublished(true)

    expect(useSecretStore.getState().maintainKeys()).toBe(false)
    // Nothing changed, so nothing needs republishing.
    expect(useSecretStore.getState().published).toBe(true)
  })

  it('rotates the signed prekey once it is older than a week', async () => {
    const before = await useSecretStore.getState().hydrate('u1')
    const stale = {
      ...before,
      signedPreKeyCreatedAt: Date.now() - SIGNED_PREKEY_MAX_AGE_MS - 1,
    }
    useSecretStore.setState({ identity: stale, published: true })

    expect(useSecretStore.getState().maintainKeys()).toBe(true)
    const after = useSecretStore.getState().identity!
    expect(toBase64(after.signedPreKey.publicKey)).not.toBe(
      toBase64(before.signedPreKey.publicKey),
    )
    // Clearing `published` is what makes the publisher send the new bundle; a
    // rotation the directory never hears about is worse than none.
    expect(useSecretStore.getState().published).toBe(false)
  })

  /**
   * Rotation is not atomic across the network: a peer may have fetched the old
   * bundle seconds earlier and be sending its first message against it. The
   * relay is fire-and-forget, so that message would simply vanish.
   */
  it('keeps the outgoing signed prekey for one generation', async () => {
    const before = await useSecretStore.getState().hydrate('u1')
    useSecretStore.setState({
      identity: { ...before, signedPreKeyCreatedAt: Date.now() - SIGNED_PREKEY_MAX_AGE_MS - 1 },
    })
    useSecretStore.getState().maintainKeys()

    const after = useSecretStore.getState().identity!
    expect(after.previousSignedPreKey).toBeDefined()
    expect(toBase64(after.previousSignedPreKey!.publicKey)).toBe(
      toBase64(before.signedPreKey.publicKey),
    )
  })

  it('refills the one-time pool when it runs low', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    useSecretStore.setState({
      identity: { ...identity, oneTimePreKeys: identity.oneTimePreKeys.slice(0, 2) },
      published: true,
    })

    expect(useSecretStore.getState().maintainKeys()).toBe(true)
    expect(useSecretStore.getState().identity!.oneTimePreKeys).toHaveLength(
      ONE_TIME_PREKEY_COUNT,
    )
    expect(useSecretStore.getState().published).toBe(false)
  })

  it('leaves a pool that is merely dented alone', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    useSecretStore.setState({
      identity: { ...identity, oneTimePreKeys: identity.oneTimePreKeys.slice(0, -1) },
    })
    expect(useSecretStore.getState().maintainKeys()).toBe(false)
  })

  it('survives a reload', async () => {
    const before = await useSecretStore.getState().hydrate('u1')
    useSecretStore.setState({
      identity: { ...before, signedPreKeyCreatedAt: Date.now() - SIGNED_PREKEY_MAX_AGE_MS - 1 },
    })
    useSecretStore.getState().maintainKeys()
    const rotated = useSecretStore.getState().identity!

    useSecretStore.getState().reset()
    const reloaded = await useSecretStore.getState().hydrate('u1')

    expect(toBase64(reloaded.signedPreKey.publicKey)).toBe(
      toBase64(rotated.signedPreKey.publicKey),
    )
    expect(toBase64(reloaded.previousSignedPreKey!.publicKey)).toBe(
      toBase64(rotated.previousSignedPreKey!.publicKey),
    )
    expect(reloaded.signedPreKeyCreatedAt).toBe(rotated.signedPreKeyCreatedAt)
  })

  /**
   * An identity written before rotation existed has no timestamp. Treating it as
   * ancient would rotate every such device on its next launch — a thundering
   * herd of republishes for a key nobody knows to be old.
   */
  it('does not immediately rotate an identity stored before rotation existed', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    const legacy = {
      identity: {
        privateKey: toBase64(identity.identity.privateKey),
        publicKey: toBase64(identity.identity.publicKey),
      },
      signing: {
        privateKey: toBase64(identity.signing.privateKey),
        publicKey: toBase64(identity.signing.publicKey),
      },
      signedPreKey: {
        privateKey: toBase64(identity.signedPreKey.privateKey),
        publicKey: toBase64(identity.signedPreKey.publicKey),
      },
      oneTimePreKeys: [],
    }
    // In localStorage, not the vault: this is exactly what an installation
    // from before either change looks like, so it also exercises the migration.
    localStorage.setItem(`SyncApp:e2e-identity:u2`, JSON.stringify(legacy))

    const loaded = await useSecretStore.getState().hydrate('u2')
    expect(loaded.signedPreKeyCreatedAt).toBeGreaterThan(Date.now() - 5_000)
    // The empty pool still gets refilled — that part IS known to be wrong.
    expect(useSecretStore.getState().maintainKeys()).toBe(true)
    expect(useSecretStore.getState().identity!.oneTimePreKeys).toHaveLength(
      ONE_TIME_PREKEY_COUNT,
    )
  })
})

/**
 * The directory's own report of this device's bundle.
 *
 * This is the half the local count cannot replace. One-time prekeys are consumed by
 * PEERS fetching bundles; the local pool shrinks only when a message decrypts with a
 * key, which misses every fetch that never became a message. Without the report the
 * directory runs dry while the device believes it is full, and X3DH silently drops
 * from four Diffie-Hellmans to three for every session started afterwards.
 */
describe('applyDirectoryState', () => {
  const state = (overrides: Partial<DirectoryKeyState> = {}): DirectoryKeyState => ({
    oneTimePrekeysLeft: ONE_TIME_PREKEY_COUNT,
    signedPrekeyAgeMs: 0,
    accepted: 0,
    ...overrides,
  })

  it('marks the accepted prekeys as offered, so they are not published twice', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    const sent = identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey))

    useSecretStore.getState().applyDirectoryState(state({ accepted: sent.length }), sent)

    // Publishing APPENDS on the server. Offering the same public key again files it
    // twice, and a one-time prekey stored twice can be handed to two peers — the one
    // thing it exists not to be.
    expect(useSecretStore.getState().unpublishedPreKeys()).toHaveLength(0)
  })

  /**
   * A truncated frame keeps its LAST `accepted` keys: the directory appends and trims
   * from the front. Marking the first ones instead would retire exactly the keys it
   * dropped and re-offer the ones it kept — the duplicate this tracking prevents, in
   * the one case where it is certain to happen.
   */
  it('retires the keys the directory kept, not the ones it dropped', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    const sent = identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey))

    useSecretStore.getState().applyDirectoryState(state({ accepted: 3 }), sent)

    const stillPending = useSecretStore
      .getState()
      .unpublishedPreKeys()
      .map((pair) => toBase64(pair.publicKey))
    expect(stillPending).toEqual(sent.slice(0, sent.length - 3))
  })

  it('mints fresh keys when the DIRECTORY is low, however full the local pool looks', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    const sent = identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey))
    useSecretStore.setState({ published: true })

    // The local pool is untouched — every key still here — while the directory has
    // served all but three. That divergence is the normal case, not an edge one.
    const republish = useSecretStore
      .getState()
      .applyDirectoryState(state({ oneTimePrekeysLeft: 3, accepted: sent.length }), sent)

    expect(republish).toBe(true)
    // Clearing `published` is what sends the new keys; minting them and keeping quiet
    // would leave the directory exactly as empty as it was.
    expect(useSecretStore.getState().published).toBe(false)
    const pending = useSecretStore.getState().unpublishedPreKeys()
    expect(pending).toHaveLength(ONE_TIME_PREKEY_COUNT - 3)
    // Fresh pairs, not the ones the directory already served.
    const offered = new Set(sent)
    expect(pending.every((pair) => !offered.has(toBase64(pair.publicKey)))).toBe(true)
  })

  it('does not republish while the directory reports a healthy bundle', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    const sent = identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey))

    const republish = useSecretStore
      .getState()
      .applyDirectoryState(state({ accepted: sent.length }), sent)

    expect(republish).toBe(false)
    expect(useSecretStore.getState().published).toBe(true)
  })

  /**
   * An unpublished remainder means the per-publish cap truncated the frame, NOT that
   * the directory needs anything. Republishing on that would send the remainder, have
   * it truncated again, and loop forever against a directory that is already full — so
   * the decision is driven by what the directory reports and nothing else.
   */
  it('does not republish merely because keys are still unoffered', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    const sent = identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey))

    const republish = useSecretStore
      .getState()
      .applyDirectoryState(state({ accepted: 1 }), sent)

    expect(republish).toBe(false)
    expect(useSecretStore.getState().unpublishedPreKeys()).toHaveLength(sent.length - 1)
  })

  /**
   * The directory is serving a signed prekey older than the rotation window. Either
   * this device never rotated, or it rotated and the publish never landed — and it
   * cannot tell which, because it cannot see what the directory holds. A republish
   * settles both: maintainKeys rotates a genuinely old local key on the next pass, and
   * a fresh one simply goes out again.
   */
  it('republishes when the directory holds a signed prekey past the rotation window', async () => {
    await useSecretStore.getState().hydrate('u1')
    useSecretStore.setState({ published: true })

    const republish = useSecretStore
      .getState()
      .applyDirectoryState(state({ signedPrekeyAgeMs: SIGNED_PREKEY_MAX_AGE_MS + 1 }), [])

    expect(republish).toBe(true)
    expect(useSecretStore.getState().published).toBe(false)
  })

  it('caps the local pool so refills cannot grow it without bound', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    // A device that has been drained repeatedly: every refill adds a batch, and the old
    // private halves are still wanted for first-messages in flight.
    useSecretStore.setState({
      identity: {
        ...identity,
        oneTimePreKeys: Array.from({ length: MAX_LOCAL_PREKEYS }, (_, i) =>
          i < identity.oneTimePreKeys.length ? identity.oneTimePreKeys[i] : generateKeyPair(),
        ),
      },
    })

    useSecretStore.getState().applyDirectoryState(state({ oneTimePrekeysLeft: 0 }), [])

    expect(useSecretStore.getState().identity!.oneTimePreKeys).toHaveLength(MAX_LOCAL_PREKEYS)
  })

  it('persists what the directory confirmed, so a reload does not re-offer it', async () => {
    const identity = await useSecretStore.getState().hydrate('u1')
    const sent = identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey))
    useSecretStore.getState().applyDirectoryState(state({ accepted: sent.length }), sent)

    useSecretStore.getState().reset()
    await useSecretStore.getState().hydrate('u1')

    expect(useSecretStore.getState().unpublishedPreKeys()).toHaveLength(0)
  })

  it('does nothing without a loaded identity', () => {
    expect(useSecretStore.getState().applyDirectoryState(state(), [])).toBe(false)
  })
})
