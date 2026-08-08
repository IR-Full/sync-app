import type { SecretIdentity } from '@/entities/secret-chat'
import { fromBase64, fromUtf8 } from '@/shared/lib/e2e/codec'
import {
  RatchetSession,
  unmarshalHeader,
  type SerializedSession,
} from '@/shared/lib/e2e/ratchet'
import { x3dhResponder } from '@/shared/lib/e2e/x3dh'

/** The X3DH bootstrap carried inside SecretMsg.ratchetHeader. */
export interface InitEnvelope {
  ik?: string
  ek?: string
  rh: string
}

export interface DecryptedSecret {
  plaintext: string
  session: SerializedSession
  /** public key of the one-time prekey the sender consumed, when one was used */
  consumedOneTimePreKey?: string
}

/**
 * Opens one inbound secret message.
 *
 * Pure and side-effect free so it can be exercised directly against a Go
 * initiator — the responder path is the half that cannot be proven by sending.
 *
 * The awkward part is the one-time prekey: the directory hands a fetcher one of
 * ours and never tells us which. So every unconsumed candidate is tried (plus
 * the no-OPK case, since the bundle may have run out), and the AEAD is the
 * oracle — only the right key authenticates. With a handful of prekeys this
 * costs a few X25519 operations on the first message of a session and nothing
 * afterwards.
 */
export function openSecretMessage(
  identity: SecretIdentity,
  existingSession: SerializedSession | undefined,
  message: { ratchetHeader: string; ciphertext: string },
): DecryptedSecret | null {
  let init: InitEnvelope
  try {
    init = JSON.parse(message.ratchetHeader) as InitEnvelope
  } catch {
    return null
  }
  if (!init?.rh) return null

  const header = unmarshalHeader(fromBase64(init.rh))
  const ciphertext = fromBase64(message.ciphertext)

  // An established session simply advances.
  if (existingSession) {
    try {
      const session = RatchetSession.deserialize(existingSession)
      const plaintext = fromUtf8(session.decrypt(header, ciphertext))
      return { plaintext, session: session.serialize() }
    } catch {
      // Fall through: the peer may have started a fresh session.
    }
  }

  // Otherwise this must be a first message, which carries the X3DH bootstrap.
  if (!init.ik || !init.ek) return null
  const initiatorIdentity = fromBase64(init.ik)
  const initiatorEphemeral = fromBase64(init.ek)

  const candidates: { oneTime?: SecretIdentity['oneTimePreKeys'][number] }[] = [
    ...identity.oneTimePreKeys.map((oneTime) => ({ oneTime })),
    {},
  ]

  for (const candidate of candidates) {
    try {
      const sharedSecret = x3dhResponder(
        {
          identity: identity.identity,
          signedPreKey: identity.signedPreKey,
          oneTimePreKey: candidate.oneTime,
        },
        initiatorIdentity,
        initiatorEphemeral,
        !!candidate.oneTime,
      )
      const session = RatchetSession.responder(sharedSecret, identity.signedPreKey)
      const plaintext = fromUtf8(session.decrypt(header, ciphertext))
      return {
        plaintext,
        session: session.serialize(),
        consumedOneTimePreKey: candidate.oneTime
          ? // Reported so the caller can forget it; a reused prekey weakens the
            // forward secrecy the one-time key exists to provide.
            toBase64Public(candidate.oneTime.publicKey)
          : undefined,
      }
    } catch {
      // Wrong candidate — try the next.
    }
  }

  return null
}

function toBase64Public(bytes: Uint8Array): string {
  let binary = ''
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i])
  return btoa(binary)
}
