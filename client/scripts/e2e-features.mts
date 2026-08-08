/**
 * Live check of the product-surface protocol calls, against a real gateway.
 *
 * Complements e2e-protocol.mts (which covers the transport and core messaging)
 * by exercising everything the UI added on top: edits, deletes, reactions,
 * media, pins, drafts, search, forwarding, scheduling, threads, polls, invites
 * and export.
 *
 * Usage:
 *   1. start the gateway:  go run ./cmd/server
 *   2. npm run test:features
 */
import { SynapseClient } from '../src/shared/api/protocol/client.ts'
import { MsgType } from '../src/shared/api/protocol/msg-type.ts'
import type * as Body from '../src/shared/api/protocol/generated/bodies.ts'

const URL_ = process.argv[2] ?? 'ws://localhost:8080/ws'
const stamp = Date.now()
const alice = `fa${stamp}`
const bob = `fb${stamp}`

let failures = 0
function check(label: string, ok: boolean, detail?: unknown) {
  console.log(
    `${ok ? 'PASS' : 'FAIL'}  ${label}${detail !== undefined ? ` — ${JSON.stringify(detail)}` : ''}`,
  )
  if (!ok) failures++
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const a = new SynapseClient({ url: URL_ })
  a.setDeviceId('feat-alice')
  await a.connect({
    kind: 'password',
    username: alice,
    password: 'correct horse battery',
    register: true,
  })

  const b = new SynapseClient({ url: URL_ })
  b.setDeviceId('feat-bob')
  const bobSession = await b.connect({
    kind: 'password',
    username: bob,
    password: 'correct horse battery',
    register: true,
  })

  const incoming: Body.NewMessage[] = []
  b.on('message', (m) => incoming.push(m))

  // --- seed a direct chat
  const ack = await a.request<Body.SendAck>(
    MsgType.SEND,
    { chatId: `@${bob}`, dedupKey: `f-${stamp}-1`, text: 'original text' },
    { expect: MsgType.SEND_ACK },
  )
  const chatId = ack.body.chatId
  const messageId = ack.body.messageId

  // --- EDIT: no reply on success; the change arrives as a NEW with edited=true
  a.send(MsgType.EDIT, { chatId, messageId, text: 'edited text' })
  await sleep(700)
  const editEcho = incoming.find((m) => m.messageId === messageId && m.edited)
  check('EDIT propagates as NEW(edited)', !!editEcho, { text: editEcho?.text })

  // --- REACT: reply carries the post-change tally
  const react = await a.request<Body.ReactUpdate>(
    MsgType.REACT,
    { chatId, messageId, emoji: '👍' },
    { expect: MsgType.REACT_UPD },
  )
  check('REACT -> REACT_UPD with counts', react.body.added && react.body.counts['👍'] === 1, {
    added: react.body.added,
    counts: react.body.counts,
  })

  // --- PIN / PIN_LIST
  await a.request<Body.Pinned>(MsgType.PIN, { chatId, messageId }, { expect: MsgType.PINNED })
  const pins = await a.request<Body.Pinned>(
    MsgType.PIN_LIST,
    { chatId, messageId: '' },
    { expect: MsgType.PINNED },
  )
  check(
    'PIN -> PINNED lists the pin',
    pins.body.pins.some((p) => p.messageId === messageId),
    {
      pins: pins.body.pins.length,
    },
  )

  // --- DRAFT_SET is fire-and-forget; DRAFT_SYNC reads it back
  a.send(MsgType.DRAFT_SET, { chatId, text: 'unsent thought', replyTo: '' })
  await sleep(400)
  const drafts = await a.request<Body.Drafts>(
    MsgType.DRAFT_SYNC,
    { since: 0 },
    { expect: MsgType.DRAFTS },
  )
  check(
    'DRAFT_SET -> DRAFT_SYNC round trip',
    drafts.body.drafts.some((d) => d.chatId === chatId && d.text === 'unsent thought'),
    drafts.body.drafts,
  )

  // --- SEARCH (the indexer is asynchronous, so allow it a moment)
  await a.request(
    MsgType.SEND,
    { chatId, dedupKey: `f-${stamp}-search`, text: 'pineapple marmalade' },
    { expect: MsgType.SEND_ACK },
  )
  await sleep(1200)
  const search = await a.request<Body.SearchResults>(
    MsgType.SEARCH,
    { query: 'pineapple', limit: 10 },
    { expect: MsgType.SEARCH_RESULTS },
  )
  check('SEARCH -> SEARCH_RESULTS', search.body.hits.length > 0, {
    hits: search.body.hits.length,
    first: search.body.hits[0]?.text,
  })

  // --- MEDIA: init ticket, upload the exact bytes, attach the ref
  const payload = new TextEncoder().encode('hello media payload')
  const ticket = await a.request<Body.MediaTicket>(
    MsgType.MEDIA_INIT,
    { filename: 'note.txt', contentType: 'text/plain', size: payload.byteLength },
    { expect: MsgType.MEDIA_TICKET },
  )
  const put = await fetch(ticket.body.uploadUrl, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/plain' },
    body: payload,
  })
  check('MEDIA_INIT + upload', put.status === 201, { status: put.status })

  const withMedia = await a.request<Body.SendAck>(
    MsgType.SEND,
    {
      chatId,
      dedupKey: `f-${stamp}-media`,
      text: 'see attachment',
      mediaRef: ticket.body.mediaRef,
      attachment: {
        kind: 'file',
        mediaRef: ticket.body.mediaRef,
        filename: 'note.txt',
        mime: 'text/plain',
        size: payload.byteLength,
      },
    },
    { expect: MsgType.SEND_ACK },
  )
  check('SEND with attachment', !!withMedia.body.messageId)

  const mediaUrl = await a.request<Body.MediaURL>(
    MsgType.MEDIA_FETCH,
    { mediaRef: ticket.body.mediaRef },
    { expect: MsgType.MEDIA_URL },
  )
  const fetched = await fetch(mediaUrl.body.downloadUrl)
  const body = await fetched.text()
  check('MEDIA_FETCH -> signed download works', body === 'hello media payload', {
    status: fetched.status,
  })

  // --- FORWARD answers with a SEND_ACK for the new copy
  const group = await a.request<Body.ChatInfo>(
    MsgType.CHAT_CREATE,
    { type: 'group', title: 'feature group', members: [`@${bob}`] },
    { expect: MsgType.CHAT_INFO },
  )
  const forwarded = await a.request<Body.SendAck>(
    MsgType.FORWARD,
    {
      fromChatId: chatId,
      messageId,
      toChatId: group.body.chatId,
      dedupKey: `f-${stamp}-fwd`,
    },
    { expect: MsgType.SEND_ACK },
  )
  check('FORWARD -> SEND_ACK in the destination', forwarded.body.chatId === group.body.chatId, {
    chatId: forwarded.body.chatId,
  })

  // --- SCHEDULE / list / cancel
  const scheduled = await a.request<Body.Scheduled>(
    MsgType.SCHEDULE,
    { chatId, text: 'later', sendAt: Date.now() + 3_600_000 },
    { expect: MsgType.SCHEDULED },
  )
  const list = await a.request<Body.Scheduled>(
    MsgType.SCHEDULE_LIST,
    { chatId },
    { expect: MsgType.SCHEDULED },
  )
  check('SCHEDULE + SCHEDULE_LIST', list.body.items.length > 0, {
    created: scheduled.body.items.length,
    listed: list.body.items.length,
  })
  if (list.body.items[0]) {
    await a.request<Body.Scheduled>(
      MsgType.SCHEDULE_CANCEL,
      { id: list.body.items[0].id },
      { expect: MsgType.SCHEDULED },
    )
    const after = await a.request<Body.Scheduled>(
      MsgType.SCHEDULE_LIST,
      { chatId },
      { expect: MsgType.SCHEDULED },
    )
    check('SCHEDULE_CANCEL removes it', after.body.items.length < list.body.items.length, {
      before: list.body.items.length,
      after: after.body.items.length,
    })
  }

  // --- THREAD: reply to a root, then read the branch forward
  await a.request(
    MsgType.SEND,
    { chatId, dedupKey: `f-${stamp}-reply`, text: 'a reply', replyTo: messageId },
    { expect: MsgType.SEND_ACK },
  )
  const thread = await a.requestStream<Body.NewMessage, Body.ThreadOK>(
    MsgType.THREAD,
    { chatId, rootId: messageId, afterSeq: 0, limit: 20 },
    { itemType: MsgType.NEW, endType: MsgType.THREAD_OK },
  )
  check('THREAD streams replies then THREAD_OK', thread.items.length > 0, {
    replies: thread.items.length,
    done: thread.end.done,
  })

  // --- POLLS
  const poll = await a.request<Body.PollState>(
    MsgType.POLL_CREATE,
    { chatId, question: 'Tabs or spaces?', options: ['Tabs', 'Spaces'], multiChoice: false },
    { expect: MsgType.POLL_STATE },
  )
  const voted = await b.request<Body.PollState>(
    MsgType.POLL_VOTE,
    { pollId: poll.body.pollId, option: 1 },
    { expect: MsgType.POLL_STATE },
  )
  check('POLL_CREATE + POLL_VOTE tallies', voted.body.totalVotes === 1, {
    options: voted.body.options,
    myVotes: voted.body.myVotes,
  })
  const closed = await a.request<Body.PollState>(
    MsgType.POLL_CLOSE,
    { pollId: poll.body.pollId },
    { expect: MsgType.POLL_STATE },
  )
  check('POLL_CLOSE', closed.body.closed === true)

  // --- INVITES + handle + role
  const invite = await a.request<Body.Invites>(
    MsgType.INVITE_CREATE,
    { chatId: group.body.chatId, maxUses: 5, expiresAt: 0 },
    { expect: MsgType.INVITES },
  )
  check('INVITE_CREATE mints a link', !!invite.body.links[0]?.code, invite.body.links[0])

  const handle = `feat${stamp}`.slice(0, 20)
  await a.request<Body.Invites>(
    MsgType.SET_USERNAME,
    { chatId: group.body.chatId, username: handle },
    { expect: MsgType.INVITES },
  )
  const joined = await b.request<Body.Invites>(
    MsgType.JOIN,
    { handle },
    { expect: MsgType.INVITES },
  )
  check('SET_USERNAME + JOIN by handle', !!joined.body.joinedChat, {
    joined: joined.body.joinedChat,
  })

  // --- EXPORT streams pages terminated by done=true
  const dump = await a.requestStream<Body.ChatExportResult, Body.ChatExportResult>(
    MsgType.CHAT_EXPORT,
    { chatId },
    {
      itemType: MsgType.CHAT_EXPORT_RESULT,
      endType: MsgType.CHAT_EXPORT_RESULT,
      isTerminal: (body) => (body as Body.ChatExportResult).done === true,
      timeoutMs: 30_000,
    },
  )
  const exported = dump.items.flatMap((page) => page.messages ?? [])
  check('CHAT_EXPORT streams pages then done', exported.length > 0 && dump.end.done === true, {
    header: dump.items[0]?.title || dump.items[0]?.type,
    messages: exported.length,
  })

  // --- CALLS: signalling only; the server relays SDP/ICE and never parses it
  const ringing: Body.CallState[] = []
  const signals: Body.CallSignal[] = []
  b.on('callState', (state) => ringing.push(state))
  b.on('callSignal', (signal) => signals.push(signal))

  const invited = await a.request<Body.CallState>(
    MsgType.CALL_INVITE,
    { chatId, kind: 'audio' },
    { expect: MsgType.CALL_STATE },
  )
  const callId = invited.body.callId
  check(
    'CALL_INVITE -> CALL_STATE(ringing)',
    invited.body.state === 'ringing' &&
      invited.body.participants.some(
        (p) => p.userId === bobSession.userId && p.state === 'invited',
      ),
    { state: invited.body.state, participants: invited.body.participants },
  )

  await sleep(600)
  check(
    'callee is rung via fanout',
    ringing.some((s) => s.callId === callId),
    {
      pushes: ringing.length,
    },
  )

  const accepted = await b.request<Body.CallState>(
    MsgType.CALL_ACCEPT,
    { callId },
    { expect: MsgType.CALL_STATE },
  )
  check('CALL_ACCEPT -> CALL_STATE(active)', accepted.body.state === 'active', {
    state: accepted.body.state,
  })

  // Relay one opaque payload; the server stamps the sender for us.
  a.send(MsgType.CALL_SIGNAL, {
    callId,
    toUserId: bobSession.userId,
    toDeviceId: '',
    signalType: 'offer',
    payload: JSON.stringify({ type: 'offer', sdp: 'v=0...' }),
  })
  await sleep(600)
  const relayed = signals.find((s) => s.signalType === 'offer')
  check('CALL_SIGNAL relayed with sender stamped', !!relayed && relayed.fromUserId !== '', {
    from: relayed?.fromUserId,
    type: relayed?.signalType,
    payloadKept: relayed?.payload?.includes('v=0'),
  })

  const ended = await b.request<Body.CallState>(
    MsgType.CALL_HANGUP,
    { callId },
    { expect: MsgType.CALL_STATE },
  )
  check('CALL_HANGUP -> CALL_STATE', ended.body.callId === callId, { state: ended.body.state })

  // --- DELETE last, so it does not disturb the checks above
  a.send(MsgType.DELETE, { chatId, messageId, forAll: true })
  await sleep(700)
  const deleteEcho = incoming.find((m) => m.messageId === messageId && m.deleted)
  check('DELETE propagates as NEW(deleted)', !!deleteEcho)

  a.close()
  b.close()

  console.log(failures === 0 ? '\nALL FEATURE CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`)
  process.exit(failures === 0 ? 0 : 1)
}

main().catch((error) => {
  console.error('feature run failed:', error)
  process.exit(1)
})
