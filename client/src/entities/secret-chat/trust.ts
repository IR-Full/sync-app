'use client'

import { create } from 'zustand'

import { fromBase64, toBase64 } from '@/shared/lib/e2e/codec'
import { readStorage, writeStorage } from '@/shared/lib/storage'

/**
 * Trust-on-first-use pinning of peer identity keys.
 *
 * A safety number lets two people DETECT a swapped identity key — if they think
 * to compare one. Almost nobody does. Pinning is the half that does not depend
 * on anyone remembering: the first time a peer device is seen its identity keys
 * are recorded, and every later session with that device is checked against the
 * record.
 *
 * Its limits are worth stating rather than implying. It cannot protect a first
 * contact: a directory that lies from the very beginning is believed. What it
 * does is turn the attack window from "every session, forever" into "one
 * moment" — a server that later starts handing out its own keys is caught
 * immediately, on every existing conversation at once, which is the realistic
 * shape of the threat.
 *
 * And a changed key is NOT automatically an attack. Reinstalls happen, devices
 * get wiped, people switch phones. So this reports and the human decides; it
 * does not block on its own. A port of `server/pkg/e2e/trust.go`, which until
 * now protected the Go reference client and nothing else.
 */

export interface PinnedIdentity {
  userId: string
  deviceId: string
  /** base64 X25519 identity public key */
  identityKey: string
  /** base64 Ed25519 identity signing public key */
  signingKey: string
  firstSeen: number
}

/** What a verification found. */
export type TrustVerdict =
  /** never seen before — the caller should pin after the session is established */
  | { kind: 'first-use' }
  /** the keys match what was pinned */
  | { kind: 'known' }
  /** the keys differ from the pin — show the safety number and let the human choose */
  | { kind: 'changed'; pinned: PinnedIdentity }

interface TrustState {
  ownerId: string | null
  /** "userId:deviceId" -> pin */
  pins: Record<string, PinnedIdentity>

  load: (ownerId: string) => void
  verify: (
    userId: string,
    deviceId: string,
    identityKey: string,
    signingKey: string,
  ) => TrustVerdict
  /** Records or replaces a pin. Replacing one should follow a human confirming. */
  accept: (userId: string, deviceId: string, identityKey: string, signingKey: string) => void
  forget: (userId: string, deviceId: string) => void
  reset: () => void
}

function pinKey(userId: string, deviceId: string): string {
  return `${userId}:${deviceId}`
}

function storageKey(ownerId: string): string {
  return `SyncApp:e2e-pins:${ownerId}`
}

/**
 * Constant-time comparison of two base64 keys.
 *
 * These are public keys, so a timing leak is not a key compromise — but the
 * comparison still tells an attacker which prefix of a forged key is correct,
 * which is a free hint to anyone grinding one. The Go side reasons the same way.
 */
function equalKeys(a: string, b: string): boolean {
  let left: Uint8Array
  let right: Uint8Array
  try {
    left = fromBase64(a)
    right = fromBase64(b)
  } catch {
    return false
  }
  if (left.length !== right.length) return false
  let diff = 0
  for (let i = 0; i < left.length; i++) diff |= left[i] ^ right[i]
  return diff === 0
}

export const useTrustStore = create<TrustState>((set, get) => ({
  ownerId: null,
  pins: {},

  load: (ownerId) => {
    set({
      ownerId,
      pins: readStorage<Record<string, PinnedIdentity>>(storageKey(ownerId), {}),
    })
  },

  verify: (userId, deviceId, identityKey, signingKey) => {
    const pinned = get().pins[pinKey(userId, deviceId)]
    if (!pinned) return { kind: 'first-use' }
    if (
      equalKeys(pinned.identityKey, identityKey) &&
      equalKeys(pinned.signingKey, signingKey)
    ) {
      return { kind: 'known' }
    }
    return { kind: 'changed', pinned }
  },

  accept: (userId, deviceId, identityKey, signingKey) => {
    const { ownerId, pins } = get()
    const next = {
      ...pins,
      [pinKey(userId, deviceId)]: {
        userId,
        deviceId,
        identityKey,
        signingKey,
        firstSeen: Date.now(),
      },
    }
    if (ownerId) writeStorage(storageKey(ownerId), next)
    set({ pins: next })
  },

  forget: (userId, deviceId) => {
    const { ownerId, pins } = get()
    const next = { ...pins }
    delete next[pinKey(userId, deviceId)]
    if (ownerId) writeStorage(storageKey(ownerId), next)
    set({ pins: next })
  },

  reset: () => set({ ownerId: null, pins: {} }),
}))

/** Re-exported so callers can store what the wire gave them without re-encoding. */
export { toBase64 }
