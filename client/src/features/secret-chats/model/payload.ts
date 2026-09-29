/**
 * The two shapes of a secret message's ratchet payload on the wire.
 *
 * Kept free of React so the interop scripts (`scripts/e2e-secret*.mts`) read a
 * SECRET_RECV exactly as the app does.
 */
import type { Wire } from '@/shared/api'
import { fromBase64, fromUtf8, toBase64, toUtf8 } from '@/shared/lib/e2e/codec'

/**
 * Builds a SECRET_SEND body with the BINARY payload fields.
 *
 * The protocol carries the ratchet payload two ways: legacy text (a JSON header
 * plus a base64 ciphertext) and raw bytes. This client advertises CapSecretQueue,
 * which is the bit the gateway reads to decide what a peer can handle — so it sends
 * bytes, and the server re-encodes for any peer that cannot.
 *
 * That saves a third of every secret message. base64 inflates by 4/3, and the
 * ciphertext is the bulk of the frame; the header stays JSON-shaped because the
 * RECEIVING CLIENT parses it, so its bytes are the UTF-8 of that JSON rather than a
 * decoded blob.
 *
 * Only ONE form is populated. Sending both would double the payload, which is the
 * opposite of the point.
 */
export function secretPayload(
  bundle: { userId?: string; deviceId: string },
  peerUserId: string,
  envelope: { ik?: string; ek?: string; rh: string },
  ciphertext: Uint8Array,
): Wire.SecretMsg {
  return {
    toUserId: bundle.userId || peerUserId,
    toDeviceId: bundle.deviceId,
    ratchetHeaderBin: toBase64(toUtf8(JSON.stringify(envelope))),
    ciphertextBin: toBase64(ciphertext),
  } as Wire.SecretMsg
}

/**
 * Reads the ratchet payload from a SECRET_RECV, whichever form it arrived in.
 *
 * Both have to be handled. The binary fields are what a current server sends this
 * client; the text ones are what an older one sends, and what arrives during a
 * rolling deploy. Preferring binary when both are set is the same rule the server
 * applies, so the two ends cannot disagree about which payload a frame carries.
 *
 * `bytes` fields cross the protobuf codec as base64 STRINGS (the codec decodes with
 * that setting), so the binary branch still decodes one — the saving is on the wire,
 * between the server and here, not in this function.
 */
export function readSecretPayload(body: Wire.SecretMsg): {
  ratchetHeader: string
  ciphertext: string
} {
  const bin = body.ratchetHeaderBin ?? ''
  const cipherBin = body.ciphertextBin ?? ''
  if (bin || cipherBin) {
    return {
      // The header is TEXT (a JSON object) that travelled as bytes, so it decodes
      // back to the string the caller parses.
      ratchetHeader: fromUtf8(fromBase64(bin)),
      // The ciphertext is opaque and stays base64 for the decryptor, which decodes
      // it itself.
      ciphertext: cipherBin,
    }
  }
  return { ratchetHeader: body.ratchetHeader ?? '', ciphertext: body.ciphertext ?? '' }
}
