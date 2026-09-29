import { chacha20poly1305 } from '@noble/ciphers/chacha.js'
import { hkdf } from '@noble/hashes/hkdf.js'
import { hmac } from '@noble/hashes/hmac.js'
import { sha256 } from '@noble/hashes/sha2.js'

import { concatBytes, fromBase64, toBase64, toUtf8 } from './codec'
import { diffieHellman, generateKeyPair, type KeyPair } from './keys'

/**
 * The Double Ratchet, ported from `server/pkg/e2e/ratchet.go`.
 *
 * Forward secrecy (a leaked key does not open past messages) plus
 * post-compromise security (a DH ratchet step heals the session). The server is
 * a blind relay throughout — it moves ciphertext and a header and can decrypt
 * neither.
 *
 * The header is authenticated, not encrypted: it travels as additional data for
 * the AEAD, so tampering with the ratchet key or counters fails decryption.
 */

export interface RatchetHeader {
  /** sender's current ratchet public key */
  dh: Uint8Array
  /** number of messages in the previous sending chain */
  pn: number
  /** message number in the current sending chain */
  n: number
}

export class DecryptError extends Error {
  constructor() {
    super('e2e: decryption failed')
    this.name = 'DecryptError'
  }
}

/** Bounds the work a hostile header can force by claiming a huge gap, per call. */
const MAX_SKIP = 1000

/**
 * Bounds how many skipped message keys a session RETAINS.
 *
 * `MAX_SKIP` only ever bounded a single call: every DH ratchet step restarts the
 * count, so the map grew without limit — and this class serialises that map into
 * localStorage, so the growth outlived the tab. Mirrors `maxSkippedKeys` in
 * `server/pkg/e2e/ratchet.constants.go`.
 */
const MAX_SKIPPED_KEYS = 2 * MAX_SKIP

/**
 * The exact serialisation a header travels as, keyed by the header object.
 *
 * This makes the AEAD's additional data literally the bytes on the wire, in both
 * directions, rather than a re-encoding that merely ought to match them. Four
 * implementations produce this header — Go, TypeScript, Kotlin, Swift — and each
 * receiver used to parse the JSON and then re-serialise it to rebuild the AD.
 * That works only while all four emit byte-identical canonical JSON: same field
 * order, no whitespace, the same base64 alphabet and padding. Nothing enforces
 * it, and the day one diverges every message between the two versions fails with
 * `DecryptError` — indistinguishable from a forgery, and pointing at nothing.
 * Carrying the bytes removes the requirement instead of documenting it.
 *
 * A WeakMap rather than a field on the header, and that is the safety property,
 * not a style choice. The bytes and the parsed counters must never disagree: the
 * ratchet drives its state from `dh`/`pn`/`n` while the AEAD verifies the bytes,
 * so a header whose fields say one thing and whose bytes say another would
 * advance the session on values nobody authenticated. A field can be rewritten —
 * `{ ...header, pn: 99 }` copies the bytes and changes the counter. An entry
 * here cannot: that spread produces a NEW object, which has no association, so
 * it falls back to encoding the counters it actually carries and the AEAD
 * rejects it. `Header.raw` in `server/pkg/e2e/ratchet.types.go` gets the same
 * guarantee from being unexported.
 *
 * Weak, so a header pins no bytes once it is unreachable.
 */
const headerBytes = new WeakMap<RatchetHeader, Uint8Array>()

/**
 * The header's wire form, and therefore the AEAD's additional data.
 *
 * A header with bytes of its own (see {@link headerBytes}) is returned as it
 * stands: one that arrived keeps what the peer signed, one this process built
 * keeps what it authenticated. Only a header with no association is encoded
 * here, and that encoding matches Go's `json.Marshal` — standard base64 for the
 * key, fields in declaration order (dh, pn, n), no whitespace. Matching still
 * matters for a header this side originates, but it is no longer the only thing
 * holding interop together.
 */
export function marshalHeader(header: RatchetHeader): Uint8Array {
  const known = headerBytes.get(header)
  if (known) return known
  return toUtf8(JSON.stringify({ dh: toBase64(header.dh), pn: header.pn, n: header.n }))
}

export function unmarshalHeader(bytes: Uint8Array): RatchetHeader {
  const parsed = JSON.parse(new TextDecoder().decode(bytes)) as {
    dh: string
    pn: number
    n: number
  }
  const header: RatchetHeader = {
    dh: fromBase64(parsed.dh),
    pn: parsed.pn ?? 0,
    n: parsed.n ?? 0,
  }
  // Copied, because the caller owns `bytes` and this association outlives the call.
  headerBytes.set(header, bytes.slice())
  return header
}

/** Derives (rootKey, chainKey) from the root key and a DH output. */
function kdfRootKey(rootKey: Uint8Array, dhOut: Uint8Array): [Uint8Array, Uint8Array] {
  const out = hkdf(sha256, dhOut, rootKey, toUtf8('SyncApp-Ratchet-RK'), 64)
  return [out.slice(0, 32), out.slice(32)]
}

/** Advances a chain key; the constants 0x02/0x01 are part of the wire contract. */
function kdfChainKey(chainKey: Uint8Array): [Uint8Array, Uint8Array] {
  const nextChainKey = hmac(sha256, chainKey, new Uint8Array([0x02]))
  const messageKey = hmac(sha256, chainKey, new Uint8Array([0x01]))
  return [nextChainKey, messageKey]
}

/**
 * Derives the AEAD key and nonce from a message key.
 *
 * A fixed nonce is safe here precisely because the key is derived per message
 * and never reused — the message key itself is deliberately not used as the
 * cipher key.
 */
function aeadFor(messageKey: Uint8Array): { key: Uint8Array; nonce: Uint8Array } {
  const buf = hkdf(sha256, messageKey, undefined, toUtf8('SyncApp-Ratchet-Msg'), 32 + 12)
  return { key: buf.slice(0, 32), nonce: buf.slice(32) }
}

function seal(messageKey: Uint8Array, ad: Uint8Array, plaintext: Uint8Array): Uint8Array {
  const { key, nonce } = aeadFor(messageKey)
  return chacha20poly1305(key, nonce, ad).encrypt(plaintext)
}

function open(messageKey: Uint8Array, ad: Uint8Array, ciphertext: Uint8Array): Uint8Array {
  const { key, nonce } = aeadFor(messageKey)
  return chacha20poly1305(key, nonce, ad).decrypt(ciphertext)
}

/** Storage key for a skipped message key. Internal only — never crosses the wire. */
function skippedKey(dhPublic: Uint8Array, n: number): string {
  return `${toBase64(dhPublic)}|${n}`
}

/** A session serialised for persistence across reloads. */
export interface SerializedSession {
  dhsPrivate: string
  dhsPublic: string
  dhr: string | null
  rootKey: string
  sendingChainKey: string | null
  receivingChainKey: string | null
  sent: number
  received: number
  previousSent: number
  skipped: Record<string, string>
  /** insertion order of `skipped`, so the oldest key is the one evicted */
  skippedOrder?: string[]
}

/**
 * One Double Ratchet session with one peer device.
 *
 * Not safe for concurrent use — callers serialise per session, exactly as the
 * Go implementation requires.
 */
/** Supplies the key pairs a session ratchets to. Tests pass recorded keys. */
export type KeySource = () => KeyPair

export class RatchetSession {
  private dhs: KeyPair
  private dhr: Uint8Array | null
  private rootKey: Uint8Array
  private sendingChainKey: Uint8Array | null = null
  private receivingChainKey: Uint8Array | null = null
  private sent = 0
  private received = 0
  private previousSent = 0
  private skipped = new Map<string, Uint8Array>()
  private skippedOrder: string[] = []
  private keySource: KeySource = generateKeyPair

  private constructor(dhs: KeyPair, dhr: Uint8Array | null, rootKey: Uint8Array) {
    this.dhs = dhs
    this.dhr = dhr
    this.rootKey = rootKey
  }

  /**
   * Initiator's session. The peer's signed prekey is the first DHr, and one DH
   * ratchet runs immediately so the initiator can send straight away.
   */
  static initiator(
    sharedSecret: Uint8Array,
    theirSignedPreKey: Uint8Array,
    keySource: KeySource = generateKeyPair,
  ): RatchetSession {
    const session = new RatchetSession(keySource(), theirSignedPreKey, sharedSecret)
    session.keySource = keySource
    const dhOut = diffieHellman(session.dhs.privateKey, theirSignedPreKey)
    const [rootKey, chainKey] = kdfRootKey(session.rootKey, dhOut)
    session.rootKey = rootKey
    session.sendingChainKey = chainKey
    return session
  }

  /**
   * Responder's session. It has no sending chain until the first message
   * arrives and triggers a ratchet step.
   */
  static responder(
    sharedSecret: Uint8Array,
    signedPreKey: KeyPair,
    keySource: KeySource = generateKeyPair,
  ): RatchetSession {
    const session = new RatchetSession(signedPreKey, null, sharedSecret)
    session.keySource = keySource
    return session
  }

  encrypt(plaintext: Uint8Array): { header: RatchetHeader; ciphertext: Uint8Array } {
    if (!this.sendingChainKey) throw new Error('e2e: no sending chain')
    const [nextChainKey, messageKey] = kdfChainKey(this.sendingChainKey)
    this.sendingChainKey = nextChainKey

    const header: RatchetHeader = {
      dh: this.dhs.publicKey,
      pn: this.previousSent,
      n: this.sent,
    }
    this.sent++
    // Pin the serialisation used as additional data to this header, so whatever
    // the caller puts on the wire is byte-for-byte what this AEAD authenticated.
    const ad = marshalHeader(header)
    headerBytes.set(header, ad)
    return { header, ciphertext: seal(messageKey, ad, plaintext) }
  }

  /**
   * Opens one inbound message.
   *
   * **No state moves until the AEAD says the message is genuine.** That ordering
   * is the security property, not a tidiness preference: the header is written
   * by whoever sent the frame, and the relay lets any account address any
   * device. The previous version ratcheted and derived skipped keys first and
   * authenticated afterwards, so a single forged frame carrying a random ratchet
   * key rewrote the session — breaking the real conversation and costing up to
   * `2 * MAX_SKIP` HMAC derivations on the way. Mirrors `Session.Decrypt` in
   * `server/pkg/e2e/ratchet.go`.
   */
  decrypt(header: RatchetHeader, ciphertext: Uint8Array): Uint8Array {
    // 1. A key stored for a message that arrived out of order. Consuming one is
    //    already commit-on-success, so it needs no staging.
    const stored = this.trySkipped(header, ciphertext)
    if (stored) return stored

    // 2. The ordinary case: the peer's current ratchet key, the next message in
    //    the chain. Only the receiving chain moves, so it is held in locals
    //    until the message authenticates — and this branch, which every in-order
    //    message takes, never pays to copy the skipped-key map.
    if (this.receivingChainKey && this.sameDhr(header.dh) && header.n === this.received) {
      const [nextChainKey, messageKey] = kdfChainKey(this.receivingChainKey)
      let plaintext: Uint8Array
      try {
        plaintext = open(messageKey, marshalHeader(header), ciphertext)
      } catch {
        throw new DecryptError()
      }
      this.receivingChainKey = nextChainKey
      this.received++
      return plaintext
    }

    // 3. Everything else — a DH ratchet step, a gap to skip, or both — runs on a
    //    COPY, adopted only if the frame authenticates. A forgery costs one
    //    discarded copy.
    const trial = RatchetSession.deserialize(this.serialize())
    trial.keySource = this.keySource
    const plaintext = trial.advance(header, ciphertext)
    this.adopt(trial)
    return plaintext
  }

  /**
   * The ratchet+skip path, run against a throwaway copy by {@link decrypt}. It
   * may mutate freely: nothing it touches is the caller's session until
   * {@link adopt} runs.
   */
  private advance(header: RatchetHeader, ciphertext: Uint8Array): Uint8Array {
    if (!this.sameDhr(header.dh)) {
      this.skipMessageKeys(header.pn)
      this.dhRatchet(header)
    }
    this.skipMessageKeys(header.n)

    if (!this.receivingChainKey) throw new DecryptError()
    const [nextChainKey, messageKey] = kdfChainKey(this.receivingChainKey)
    this.receivingChainKey = nextChainKey
    this.received++
    try {
      return open(messageKey, marshalHeader(header), ciphertext)
    } catch {
      throw new DecryptError()
    }
  }

  /** Takes over a successful trial's state. */
  private adopt(trial: RatchetSession): void {
    this.dhs = trial.dhs
    this.dhr = trial.dhr
    this.rootKey = trial.rootKey
    this.sendingChainKey = trial.sendingChainKey
    this.receivingChainKey = trial.receivingChainKey
    this.sent = trial.sent
    this.received = trial.received
    this.previousSent = trial.previousSent
    this.skipped = trial.skipped
    this.skippedOrder = trial.skippedOrder
  }

  private sameDhr(dhPublic: Uint8Array): boolean {
    if (!this.dhr || this.dhr.length !== dhPublic.length) return false
    return this.dhr.every((byte, index) => byte === dhPublic[index])
  }

  /** Advances to the peer's new ratchet key: new receiving chain, then a new sending chain. */
  private dhRatchet(header: RatchetHeader): void {
    this.previousSent = this.sent
    this.sent = 0
    this.received = 0
    this.dhr = header.dh

    const [rootKey, receivingChainKey] = kdfRootKey(
      this.rootKey,
      diffieHellman(this.dhs.privateKey, this.dhr),
    )
    this.rootKey = rootKey
    this.receivingChainKey = receivingChainKey

    this.dhs = this.keySource()
    const [nextRootKey, sendingChainKey] = kdfRootKey(
      this.rootKey,
      diffieHellman(this.dhs.privateKey, this.dhr),
    )
    this.rootKey = nextRootKey
    this.sendingChainKey = sendingChainKey
  }

  /** Stores keys for messages we have not seen yet, so a late arrival still opens. */
  private skipMessageKeys(until: number): void {
    if (!this.receivingChainKey || !this.dhr) return
    if (until - this.received > MAX_SKIP) {
      throw new Error('e2e: too many skipped messages')
    }
    while (this.received < until) {
      const [nextChainKey, messageKey] = kdfChainKey(this.receivingChainKey)
      this.receivingChainKey = nextChainKey
      this.storeSkipped(skippedKey(this.dhr, this.received), messageKey)
      this.received++
    }
  }

  /**
   * Records a message key for a gap, evicting the oldest once the store is full.
   *
   * A bound has to drop something, and the oldest is the right thing to drop: a
   * message that has not arrived after thousands of later ones is not arriving,
   * and being wrong costs one undecryptable message rather than a session.
   */
  private storeSkipped(key: string, messageKey: Uint8Array): void {
    if (!this.skipped.has(key)) this.skippedOrder.push(key)
    this.skipped.set(key, messageKey)
    while (this.skipped.size > MAX_SKIPPED_KEYS && this.skippedOrder.length > 0) {
      const oldest = this.skippedOrder.shift()
      if (oldest !== undefined) this.skipped.delete(oldest)
    }
  }

  private trySkipped(header: RatchetHeader, ciphertext: Uint8Array): Uint8Array | null {
    const key = skippedKey(header.dh, header.n)
    const messageKey = this.skipped.get(key)
    if (!messageKey) return null
    try {
      const plaintext = open(messageKey, marshalHeader(header), ciphertext)
      // A message key is single-use; keeping it would allow a replay to succeed.
      // `skippedOrder` keeps its entry: it is an eviction order, not an index,
      // and `storeSkipped` tolerates naming a key that is already gone.
      this.skipped.delete(key)
      return plaintext
    } catch {
      return null
    }
  }

  serialize(): SerializedSession {
    return {
      dhsPrivate: toBase64(this.dhs.privateKey),
      dhsPublic: toBase64(this.dhs.publicKey),
      dhr: this.dhr ? toBase64(this.dhr) : null,
      rootKey: toBase64(this.rootKey),
      sendingChainKey: this.sendingChainKey ? toBase64(this.sendingChainKey) : null,
      receivingChainKey: this.receivingChainKey ? toBase64(this.receivingChainKey) : null,
      sent: this.sent,
      received: this.received,
      previousSent: this.previousSent,
      skipped: Object.fromEntries(
        [...this.skipped].map(([key, value]) => [key, toBase64(value)]),
      ),
      skippedOrder: [...this.skippedOrder],
    }
  }

  static deserialize(state: SerializedSession): RatchetSession {
    const session = new RatchetSession(
      { privateKey: fromBase64(state.dhsPrivate), publicKey: fromBase64(state.dhsPublic) },
      state.dhr ? fromBase64(state.dhr) : null,
      fromBase64(state.rootKey),
    )
    session.sendingChainKey = state.sendingChainKey ? fromBase64(state.sendingChainKey) : null
    session.receivingChainKey = state.receivingChainKey
      ? fromBase64(state.receivingChainKey)
      : null
    session.sent = state.sent
    session.received = state.received
    session.previousSent = state.previousSent
    session.skipped = new Map(
      Object.entries(state.skipped ?? {}).map(([key, value]) => [key, fromBase64(value)]),
    )
    // A session persisted before eviction existed has no recorded order. Falling
    // back to the map's own insertion order is exactly right: `skipped` was
    // filled in ascending message number, which is the order to evict in.
    session.skippedOrder = state.skippedOrder ?? [...session.skipped.keys()]
    return session
  }
}

export { concatBytes }
