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

  // Both fields are relay-supplied strings, so neither is guaranteed to be
  // base64 (atob throws) or a well-formed header (the varint reader throws).
  // Every other rejection here returns null; a throw would escape into the
  // frame handler and take down the connection over one bad message.
  let header: ReturnType<typeof unmarshalHeader>
  let ciphertext: Uint8Array
  try {
    header = unmarshalHeader(fromBase64(init.rh))
    ciphertext = fromBase64(message.ciphertext)
  } catch {
    return null
  }

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

  // Both signed prekeys are tried, current first.
  //
  // Rotation is not atomic across the network: a peer may have fetched the
  // previous bundle seconds before this device rotated and be sending its first
  // message against it right now. The relay is fire-and-forget, so a message
  // that cannot be opened is simply lost and the sender is never told — which is
  // why the outgoing key is kept for one generation rather than discarded.
  const signedPreKeys = [identity.signedPreKey, identity.previousSignedPreKey].filter(
    (pair): pair is SecretIdentity['signedPreKey'] => pair !== undefined,
  )

  const candidates: {
    oneTime?: SecretIdentity['oneTimePreKeys'][number]
    signedPreKey: SecretIdentity['signedPreKey']
  }[] = signedPreKeys.flatMap((signedPreKey) => [
    ...identity.oneTimePreKeys.map((oneTime) => ({ oneTime, signedPreKey })),
    { signedPreKey },
  ])

  for (const candidate of candidates) {
    try {
      const sharedSecret = x3dhResponder(
        {
          identity: identity.identity,
          signedPreKey: candidate.signedPreKey,
          oneTimePreKey: candidate.oneTime,
        },
        initiatorIdentity,
        initiatorEphemeral,
        !!candidate.oneTime,
      )
      // The ratchet's first DHr must be the prekey the INITIATOR used, not
      // whichever one is current — they differ exactly in the rotation window
      // this loop exists for.
      const session = RatchetSession.responder(sharedSecret, candidate.signedPreKey)
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
