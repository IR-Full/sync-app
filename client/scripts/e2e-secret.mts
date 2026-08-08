/**
 * Cross-language interop check for secret chats.
 *
 * Proves the TypeScript X3DH + Double Ratchet port is byte-compatible with the
 * server's own `pkg/e2e`: this script is Alice (TS), the `e2epeer` Go binary is
 * Bob (Go, using the server's crypto verbatim). A message that decrypts in each
 * direction is the only convincing evidence — the two implementations agree on
 * DH ordering, HKDF info strings, the header's JSON bytes and the AEAD.
 *
 * Usage:
 *   1. start the gateway
 *   2. start the Go peer:  e2epeer.exe -user gopeerN
 *   3. npm run test:secret -- <gopeer-user-id> <gopeer-device-id>
 */
import { SynapseClient } from '../src/shared/api/protocol/client.ts'
import { MsgType } from '../src/shared/api/protocol/msg-type.ts'
import { fromBase64, toBase64, toUtf8, fromUtf8 } from '../src/shared/lib/e2e/codec.ts'
import { generateKeyPair } from '../src/shared/lib/e2e/keys.ts'
import {
  marshalHeader,
  RatchetSession,
  unmarshalHeader,
} from '../src/shared/lib/e2e/ratchet.ts'
import { x3dhInitiator } from '../src/shared/lib/e2e/x3dh.ts'
import type * as Body from '../src/shared/api/protocol/generated/bodies.ts'

const url = process.argv[2] ?? 'ws://localhost:8080/ws'
const peerUserId = process.argv[3]
const peerDeviceId = process.argv[4] ?? 'go-peer-device'

if (!peerUserId) {
  console.error('usage: e2e-secret.mts <wsUrl> <peerUserId> [peerDeviceId]')
  process.exit(2)
}

let failures = 0
function check(label: string, ok: boolean, detail?: unknown) {
  console.log(
    `${ok ? 'PASS' : 'FAIL'}  ${label}${detail !== undefined ? ` — ${JSON.stringify(detail)}` : ''}`,
  )
  if (!ok) failures++
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const alice = new SynapseClient({ url })
  alice.setDeviceId('ts-alice-device')
  await alice.connect({
    kind: 'password',
    username: `tsalice${Date.now()}`,
    password: 'correct horse battery',
    register: true,
  })

  const inbox: Body.SecretMsg[] = []
  alice.on('secret', (message) => inbox.push(message))

  // --- fetch the Go peer's bundle (it publishes on startup)
  let bundle: Body.KeyBundle | null = null
  for (let attempt = 0; attempt < 20 && !bundle; attempt++) {
    try {
      const reply = await alice.request<Body.KeyBundle>(
        MsgType.KEY_FETCH,
        { userId: peerUserId, deviceId: peerDeviceId },
        { expect: MsgType.KEY_BUNDLE },
      )
      if (reply.body.identityKey) bundle = reply.body
    } catch {
      await sleep(250)
    }
  }
  check('KEY_FETCH returns the Go peer bundle', !!bundle?.identityKey, {
    hasOneTime: !!bundle?.oneTimePrekey,
    hasSig: !!bundle?.signedPrekeySig,
  })
  if (!bundle) {
    process.exit(1)
  }

  // --- X3DH against a bundle produced by the Go implementation
  const identity = generateKeyPair()
  const ephemeral = generateKeyPair()
  const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(
    { identity, ephemeral },
    {
      identityKey: fromBase64(bundle.identityKey),
      signingKey: fromBase64(bundle.signingKey),
      signedPreKey: fromBase64(bundle.signedPrekey),
      signedPreKeySig: fromBase64(bundle.signedPrekeySig),
      oneTimePreKey: fromBase64(bundle.oneTimePrekey),
    },
  )
  check('X3DH accepts the Go-signed prekey bundle', sharedSecret.length === 32)

  const session = RatchetSession.initiator(sharedSecret, fromBase64(bundle.signedPrekey))
  const { header, ciphertext } = session.encrypt(toUtf8('привет из TypeScript'))

  await alice
    .request(
      MsgType.SECRET_SEND,
      {
        toUserId: peerUserId,
        toDeviceId: peerDeviceId,
        ratchetHeader: JSON.stringify({
          ik: toBase64(identity.publicKey),
          ek: toBase64(ephemeralPublicKey),
          rh: toBase64(marshalHeader(header)),
        }),
        ciphertext: toBase64(ciphertext),
      },
      // SECRET_SEND is fire-and-forget; the reply we care about is Bob's own
      // SECRET_RECV coming back the other way.
      { expect: MsgType.SECRET_RECV, timeoutMs: 8000 },
    )
    .catch(() => undefined)

  // --- the Go peer decrypts and replies
  for (let attempt = 0; attempt < 40 && inbox.length === 0; attempt++) await sleep(250)
  check('Go peer replied with ciphertext', inbox.length > 0, { received: inbox.length })
  if (inbox.length === 0) {
    console.log('\n(check the peer output: it prints PEER_DECRYPTED on success)')
    process.exit(1)
  }

  const reply = inbox[0]
  const replyInit = JSON.parse(reply.ratchetHeader) as { rh: string }
  const replyHeader = unmarshalHeader(fromBase64(replyInit.rh))
  const plaintext = fromUtf8(session.decrypt(replyHeader, fromBase64(reply.ciphertext)))
  check('TS decrypts the Go peer reply', plaintext === 'hello back from Go', { plaintext })

  alice.close()
  console.log(
    failures === 0 ? '\nALL SECRET-CHAT CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`,
  )
  process.exit(failures === 0 ? 0 : 1)
}

main().catch((error) => {
  console.error('secret-chat run failed:', error)
  process.exit(1)
})
