/**
 * Responder-side interop check.
 *
 * The companion script proves TS-as-initiator. This one proves the harder half:
 * a Go **initiator** (the server's own `pkg/e2e`) starts the session, and the
 * client's real `openSecretMessage` has to complete X3DH as responder — including
 * working out WHICH one-time prekey the directory handed out, which the protocol
 * never tells the publisher.
 *
 * Usage:
 *   1. start the gateway
 *   2. npm run test:secret-responder
 *      (it prints its own user/device id, then start the Go peer with
 *       -initiate-to <user>:<device>)
 */
import { openSecretMessage } from '../src/features/secret-chats/model/decrypt.ts'
import { SynapseClient } from '../src/shared/api/protocol/client.ts'
import { MsgType } from '../src/shared/api/protocol/msg-type.ts'
import { toBase64 } from '../src/shared/lib/e2e/codec.ts'
import {
  generateKeyPair,
  generateSigningKeyPair,
  signPreKey,
} from '../src/shared/lib/e2e/keys.ts'
import type * as Body from '../src/shared/api/protocol/generated/bodies.ts'

const url = process.argv[2] ?? 'ws://localhost:8080/ws'
const expected = process.argv[3] ?? 'initiated from Go'

let failures = 0
function check(label: string, ok: boolean, detail?: unknown) {
  console.log(
    `${ok ? 'PASS' : 'FAIL'}  ${label}${detail !== undefined ? ` — ${JSON.stringify(detail)}` : ''}`,
  )
  if (!ok) failures++
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const client = new SynapseClient({ url })
  client.setDeviceId('ts-responder')
  const session = await client.connect({
    kind: 'password',
    username: `tsresp${Date.now()}`,
    password: 'correct horse battery',
    register: true,
  })

  // --- build and publish a bundle exactly as the app does
  const identity = {
    identity: generateKeyPair(),
    signing: generateSigningKeyPair(),
    signedPreKey: generateKeyPair(),
    oneTimePreKeys: Array.from({ length: 8 }, () => generateKeyPair()),
  }

  client.send(MsgType.KEY_PUBLISH, {
    identityKey: toBase64(identity.identity.publicKey),
    signingKey: toBase64(identity.signing.publicKey),
    signedPrekey: toBase64(identity.signedPreKey.publicKey),
    signedPrekeySig: toBase64(
      signPreKey(identity.signing.privateKey, identity.signedPreKey.publicKey),
    ),
    prekeys: identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey)),
  })

  const inbox: Body.SecretMsg[] = []
  client.on('secret', (message) => inbox.push(message))

  console.log(`READY ${session.userId}:${session.deviceId}`)
  console.log(
    '(now run: e2epeer.exe -user <name> -initiate-to ' +
      `${session.userId}:${session.deviceId})`,
  )

  for (let attempt = 0; attempt < 120 && inbox.length === 0; attempt++) await sleep(500)
  check('received a Go-initiated secret message', inbox.length > 0)
  if (inbox.length === 0) process.exit(1)

  const message = inbox[0]
  const opened = openSecretMessage(identity, undefined, message)

  check('X3DH responder found the consumed one-time prekey', !!opened?.consumedOneTimePreKey, {
    consumed: opened?.consumedOneTimePreKey?.slice(0, 12),
  })
  check('decrypted the Go initiator plaintext', opened?.plaintext === expected, {
    plaintext: opened?.plaintext,
  })

  client.close()
  console.log(
    failures === 0 ? '\nALL RESPONDER CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`,
  )
  process.exit(failures === 0 ? 0 : 1)
}

main().catch((error) => {
  console.error('responder run failed:', error)
  process.exit(1)
})
