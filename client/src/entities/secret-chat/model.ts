'use client'

import { create } from 'zustand'

import { readStorage, writeStorage } from '@/shared/lib/storage'
import { toBase64, fromBase64 } from '@/shared/lib/e2e/codec'
import {
  generateKeyPair,
  generateSigningKeyPair,
  type KeyPair,
  type SigningKeyPair,
} from '@/shared/lib/e2e/keys'
import type { SerializedSession } from '@/shared/lib/e2e/ratchet'

/** How many one-time prekeys we publish per device. */
export const ONE_TIME_PREKEY_COUNT = 8

export interface SecretIdentity {
  identity: KeyPair
  signing: SigningKeyPair
  signedPreKey: KeyPair
  /** unconsumed one-time prekeys, kept so we can complete X3DH as responder */
  oneTimePreKeys: KeyPair[]
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
  oneTimePreKeys: { privateKey: string; publicKey: string }[]
}

interface SecretState {
  ownerId: string | null
  identity: SecretIdentity | null
  /** "userId:deviceId" -> serialised ratchet state */
  sessions: Record<string, SerializedSession>
  /** peer user id -> local transcript (the server stores nothing) */
  transcripts: Record<string, SecretMessage[]>
  published: boolean

  load: (ownerId: string) => SecretIdentity
  setPublished: (published: boolean) => void
  saveSession: (key: string, session: SerializedSession) => void
  dropOneTimePreKey: (publicKey: string) => void
  appendMessage: (message: SecretMessage) => void
  reset: () => void
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
  return `synapse:e2e-identity:${ownerId}`
}
function sessionsKey(ownerId: string) {
  return `synapse:e2e-sessions:${ownerId}`
}
function transcriptsKey(ownerId: string) {
  return `synapse:e2e-transcripts:${ownerId}`
}

function createIdentity(): SecretIdentity {
  return {
    identity: generateKeyPair(),
    signing: generateSigningKeyPair(),
    signedPreKey: generateKeyPair(),
    oneTimePreKeys: Array.from({ length: ONE_TIME_PREKEY_COUNT }, () => generateKeyPair()),
  }
}

/**
 * Long-term secret-chat material for this device.
 *
 * **Private keys live in localStorage.** That is a real weakness and worth
 * naming: anything that can run script in this origin can read them. The browser
 * offers no better option here — non-extractable WebCrypto keys cannot be used,
 * because the ratchet needs ChaCha20-Poly1305 and raw key bytes, neither of
 * which WebCrypto provides. It is the same exposure as the session token, and
 * the reason secret chats are labelled as device-bound rather than account-bound.
 *
 * Transcripts are local too: the server relays secret messages and stores
 * nothing, so history exists only on the devices that received it.
 */
export const useSecretStore = create<SecretState>((set, get) => ({
  ownerId: null,
  identity: null,
  sessions: {},
  transcripts: {},
  published: false,

  load: (ownerId) => {
    const storedIdentity = readStorage<StoredIdentity | null>(identityKey(ownerId), null)
    let identity: SecretIdentity

    if (storedIdentity) {
      identity = {
        identity: decodePair(storedIdentity.identity),
        signing: decodePair(storedIdentity.signing),
        signedPreKey: decodePair(storedIdentity.signedPreKey),
        oneTimePreKeys: (storedIdentity.oneTimePreKeys ?? []).map(decodePair),
      }
    } else {
      identity = createIdentity()
      writeStorage(identityKey(ownerId), {
        identity: encodePair(identity.identity),
        signing: encodePair(identity.signing),
        signedPreKey: encodePair(identity.signedPreKey),
        oneTimePreKeys: identity.oneTimePreKeys.map(encodePair),
      } satisfies StoredIdentity)
    }

    set({
      ownerId,
      identity,
      sessions: readStorage<Record<string, SerializedSession>>(sessionsKey(ownerId), {}),
      transcripts: readStorage<Record<string, SecretMessage[]>>(transcriptsKey(ownerId), {}),
      published: false,
    })
    return identity
  },

  setPublished: (published) => set({ published }),

  saveSession: (key, session) => {
    const { ownerId, sessions } = get()
    const next = { ...sessions, [key]: session }
    if (ownerId) writeStorage(sessionsKey(ownerId), next)
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
    writeStorage(identityKey(ownerId), {
      identity: encodePair(next.identity),
      signing: encodePair(next.signing),
      signedPreKey: encodePair(next.signedPreKey),
      oneTimePreKeys: next.oneTimePreKeys.map(encodePair),
    } satisfies StoredIdentity)
    set({ identity: next })
  },

  appendMessage: (message) => {
    const { ownerId, transcripts } = get()
    const forPeer = [...(transcripts[message.peerId] ?? []), message]
    const next = { ...transcripts, [message.peerId]: forPeer }
    if (ownerId) writeStorage(transcriptsKey(ownerId), next)
    set({ transcripts: next })
  },

  reset: () =>
    set({ ownerId: null, identity: null, sessions: {}, transcripts: {}, published: false }),
}))

export function useSecretTranscript(peerId: string): SecretMessage[] {
  return useSecretStore((state) => state.transcripts[peerId] ?? EMPTY)
}

const EMPTY: SecretMessage[] = []
