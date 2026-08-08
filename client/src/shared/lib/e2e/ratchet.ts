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

/** Bounds the work a hostile header can force by claiming a huge gap. */
const MAX_SKIP = 1000

/**
 * Serialises a header exactly as Go's `json.Marshal` does.
 *
 * This is the AEAD's additional data, so the bytes must match the Go side
 * character for character. Two details carry the whole compatibility:
 * Go renders a `[]byte` field as standard base64, and it emits struct fields in
 * declaration order (dh, pn, n) with no whitespace.
 */
export function marshalHeader(header: RatchetHeader): Uint8Array {
  return toUtf8(JSON.stringify({ dh: toBase64(header.dh), pn: header.pn, n: header.n }))
}

export function unmarshalHeader(bytes: Uint8Array): RatchetHeader {
  const parsed = JSON.parse(new TextDecoder().decode(bytes)) as {
    dh: string
    pn: number
    n: number
  }
  return { dh: fromBase64(parsed.dh), pn: parsed.pn ?? 0, n: parsed.n ?? 0 }
}

/** Derives (rootKey, chainKey) from the root key and a DH output. */
function kdfRootKey(rootKey: Uint8Array, dhOut: Uint8Array): [Uint8Array, Uint8Array] {
  const out = hkdf(sha256, dhOut, rootKey, toUtf8('Synapse-Ratchet-RK'), 64)
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
  const buf = hkdf(sha256, messageKey, undefined, toUtf8('Synapse-Ratchet-Msg'), 32 + 12)
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
}

/**
 * One Double Ratchet session with one peer device.
 *
 * Not safe for concurrent use — callers serialise per session, exactly as the
 * Go implementation requires.
 */
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

  private constructor(dhs: KeyPair, dhr: Uint8Array | null, rootKey: Uint8Array) {
    this.dhs = dhs
    this.dhr = dhr
    this.rootKey = rootKey
  }

  /**
   * Initiator's session. The peer's signed prekey is the first DHr, and one DH
   * ratchet runs immediately so the initiator can send straight away.
   */
  static initiator(sharedSecret: Uint8Array, theirSignedPreKey: Uint8Array): RatchetSession {
    const session = new RatchetSession(generateKeyPair(), theirSignedPreKey, sharedSecret)
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
  static responder(sharedSecret: Uint8Array, signedPreKey: KeyPair): RatchetSession {
    return new RatchetSession(signedPreKey, null, sharedSecret)
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
    return { header, ciphertext: seal(messageKey, marshalHeader(header), plaintext) }
  }

  decrypt(header: RatchetHeader, ciphertext: Uint8Array): Uint8Array {
    // 1. A key stored for a message that arrived out of order.
    const stored = this.trySkipped(header, ciphertext)
    if (stored) return stored

    // 2. A new ratchet key means the peer stepped; follow them.
    if (!this.sameDhr(header.dh)) {
      this.skipMessageKeys(header.pn)
      this.dhRatchet(header)
    }

    // 3. Catch up on anything missing earlier in this chain.
    this.skipMessageKeys(header.n)

    // 4. Derive this message's key and open it.
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

    this.dhs = generateKeyPair()
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
      this.skipped.set(skippedKey(this.dhr, this.received), messageKey)
      this.received++
    }
  }

  private trySkipped(header: RatchetHeader, ciphertext: Uint8Array): Uint8Array | null {
    const key = skippedKey(header.dh, header.n)
    const messageKey = this.skipped.get(key)
    if (!messageKey) return null
    try {
      const plaintext = open(messageKey, marshalHeader(header), ciphertext)
      // A message key is single-use; keeping it would allow a replay to succeed.
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
    return session
  }
}

export { concatBytes }
