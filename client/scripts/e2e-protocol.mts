/**
 * Live protocol check against a real Synapse gateway.
 *
 * This exercises the client's wire layer end to end — framing, varint envelope,
 * protobuf bodies, handshake, auth, send/ack, history paging, receipts — against
 * the actual Go server rather than a mock, which is the only way to be sure the
 * byte layout matches.
 *
 * Usage:
 *   1. start the gateway:  server.exe            (defaults to :8080 for /ws)
 *   2. node scripts/e2e-protocol.mts [ws://localhost:8080/ws]
 */
import { SynapseClient } from '../src/shared/api/protocol/client.ts'
import { MsgType } from '../src/shared/api/protocol/msg-type.ts'
import type * as Body from '../src/shared/api/protocol/generated/bodies.ts'

const URL_ = process.argv[2] ?? 'ws://localhost:8080/ws'
const stamp = Date.now()
const alice = `alice${stamp}`
const bob = `bob${stamp}`

let failures = 0
function check(label: string, ok: boolean, detail?: unknown) {
  console.log(
    `${ok ? 'PASS' : 'FAIL'}  ${label}${detail !== undefined ? ` — ${JSON.stringify(detail)}` : ''}`,
  )
  if (!ok) failures++
}

async function main() {
  // --- register two accounts over the binary protocol (there is no REST signup)
  const a = new SynapseClient({ url: URL_ })
  a.setDeviceId('e2e-alice')
  const aliceSession = await a.connect({
    kind: 'password',
    username: alice,
    password: 'correct horse battery',
    register: true,
  })
  check('register + auth (alice)', !!aliceSession.userId && !!aliceSession.token, {
    userId: aliceSession.userId,
    hasResume: !!aliceSession.resumeToken,
  })

  const b = new SynapseClient({ url: URL_ })
  b.setDeviceId('e2e-bob')
  const bobSession = await b.connect({
    kind: 'password',
    username: bob,
    password: 'correct horse battery',
    register: true,
  })
  check('register + auth (bob)', !!bobSession.userId, { userId: bobSession.userId })

  // --- bob listens for live fanout
  const inbox: Body.NewMessage[] = []
  const typingSeen: Body.Typing[] = []
  b.on('message', (m) => inbox.push(m))
  b.on('typing', (t) => typingSeen.push(t))

  // --- alice sends to "@bob": the gateway resolves the handle and creates the
  //     canonical 1:1 chat on the fly (there is no create-direct-chat message).
  const ack = await a.request<Body.SendAck>(
    MsgType.SEND,
    { chatId: `@${bob}`, dedupKey: `e2e-${stamp}-1`, text: 'hello from the web client' },
    { expect: MsgType.SEND_ACK },
  )
  check('SEND -> SEND_ACK', !!ack.body.messageId && ack.body.chatSeq > 0, {
    messageId: ack.body.messageId,
    chatId: ack.body.chatId,
    chatSeq: ack.body.chatSeq,
    duplicate: ack.body.duplicate,
  })
  const chatId = ack.body.chatId

  // --- idempotency: the same dedup key must resolve to the same message
  const dup = await a.request<Body.SendAck>(
    MsgType.SEND,
    { chatId: `@${bob}`, dedupKey: `e2e-${stamp}-1`, text: 'hello from the web client' },
    { expect: MsgType.SEND_ACK },
  )
  check(
    'dedup key is idempotent',
    dup.body.duplicate && dup.body.messageId === ack.body.messageId,
    {
      duplicate: dup.body.duplicate,
      sameId: dup.body.messageId === ack.body.messageId,
    },
  )

  // --- live delivery to bob
  await sleep(600)
  check(
    'live fanout NEW reached bob',
    inbox.some((m) => m.messageId === ack.body.messageId),
    {
      received: inbox.length,
      text: inbox[0]?.text,
    },
  )

  // --- typing indicator (fire-and-forget, best effort)
  a.send(MsgType.TYPING, { chatId, active: true })
  await sleep(400)
  check('TYPING relayed', typingSeen.length > 0, typingSeen[0])

  // --- a few more messages, then paged history
  for (let i = 2; i <= 5; i++) {
    await a.request(
      MsgType.SEND,
      { chatId, dedupKey: `e2e-${stamp}-${i}`, text: `message ${i}` },
      { expect: MsgType.SEND_ACK },
    )
  }
  const page = await a.requestStream<Body.NewMessage, Body.HistoryOK>(
    MsgType.HISTORY,
    { chatId, beforeSeq: 0, limit: 3 },
    { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK },
  )
  check(
    'HISTORY streams NEW frames then HISTORY_OK',
    page.items.length === 3 && !page.end.done,
    {
      got: page.items.length,
      nextBefore: page.end.nextBefore,
      done: page.end.done,
    },
  )

  const older = await a.requestStream<Body.NewMessage, Body.HistoryOK>(
    MsgType.HISTORY,
    { chatId, beforeSeq: page.end.nextBefore, limit: 3 },
    { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK },
  )
  check('HISTORY cursor pages backwards', older.items.length > 0 && older.end.done, {
    got: older.items.length,
    done: older.end.done,
  })

  // --- read receipt: bob marks read, alice hears about it
  const receipts: Body.ReadUpdate[] = []
  a.on('read', (r) => receipts.push(r))
  b.send(MsgType.READ, { chatId, upToChatSeq: ack.body.chatSeq })
  await sleep(600)
  check('READ -> READ_UPD to the other party', receipts.length > 0, receipts[0])

  // --- group creation
  const group = await a.request<Body.ChatInfo>(
    MsgType.CHAT_CREATE,
    { type: 'group', title: 'e2e group', members: [`@${bob}`] },
    { expect: MsgType.CHAT_INFO },
  )
  check(
    'CHAT_CREATE -> CHAT_INFO',
    group.body.chatId !== '' && group.body.type === 'group',
    group.body,
  )

  // --- contacts round trip
  await a.request<Body.ContactList>(
    MsgType.CONTACT_ADD,
    { target: `@${bob}`, name: 'Bob' },
    { expect: MsgType.CONTACT_LIST },
  )
  const contacts = await a.request<Body.ContactList>(
    MsgType.CONTACT_SYNC,
    { since: 0 },
    { expect: MsgType.CONTACT_LIST },
  )
  check('CONTACT_ADD + CONTACT_SYNC', contacts.body.contacts.length > 0, contacts.body.contacts)

  // --- token re-auth on a fresh connection (what "auto login" will do)
  const c = new SynapseClient({ url: URL_ })
  c.setDeviceId('e2e-alice')
  const resumed = await c.connect({ kind: 'token', token: aliceSession.token })
  check('re-auth with stored token', resumed.userId === aliceSession.userId, {
    userId: resumed.userId,
  })

  // --- error mapping: a bad chat id must come back as a typed ProtocolError
  let errorCode: number | null = null
  try {
    await c.request(
      MsgType.SEND,
      { chatId: 'not-a-snowflake', dedupKey: 'x', text: 'x' },
      {
        expect: MsgType.SEND_ACK,
      },
    )
  } catch (error) {
    errorCode = (error as { code?: number }).code ?? null
  }
  check('invalid chat id rejects with a protocol error', errorCode !== null, {
    code: errorCode,
  })

  a.close()
  b.close()
  c.close()

  console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`)
  process.exit(failures === 0 ? 0 : 1)
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

main().catch((error) => {
  console.error('e2e run failed:', error)
  process.exit(1)
})
