import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { Cap, CLIENT_CAPS } from './caps'
import { SyncAppClient, type ConnectionState, type Session } from './client'
import { ErrorCode, ProtocolError } from './error-code'
import { FakeSocket } from './fake-socket'
import { encodeFrame } from './frame'
import { MsgType } from './msg-type'

const URL = 'ws://gateway.test/ws'

/**
 * Drains the microtask queue so an `await` chain inside the client can advance.
 *
 * Deliberately not `setTimeout(…, 0)`: half these tests install fake timers, and
 * a timer-based flush would simply never fire under them.
 */
async function tick(): Promise<void> {
  for (let i = 0; i < 12; i++) await Promise.resolve()
}

/**
 * Replies to the client's most recent request.
 *
 * The request counter is per-client, not per-connection, so a redial's HELLO is
 * request 3, not request 1 — hard-coding an id would silently fail to correlate
 * and the test would hang rather than fail.
 */
function replyToLast(socket: FakeSocket, type: number, body?: object | null): void {
  socket.deliver(type, body, { requestId: socket.envelopes.at(-1)!.requestId })
}

const WELCOME = {
  serverVersion: 'test/1',
  sessionId: 's1',
  caps: CLIENT_CAPS,
  heartbeatMs: 20_000,
  maxInflight: 32,
  resumeSupported: true,
}

const AUTH_OK = {
  userId: 'u1',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'tok-1',
  resumeToken: 'resume-1',
  username: 'alice',
  displayName: 'Alice',
  avatarRef: '',
}

/**
 * Walks a fresh client through dial → HELLO/WELCOME → AUTH/AUTH_OK, which is the
 * precondition for almost everything else and is far too long to repeat inline.
 * Returns once `connect()` has resolved, i.e. once the client is actually usable.
 */
async function connected(
  options: { welcome?: Partial<typeof WELCOME>; deviceId?: string } = {},
): Promise<{ client: SyncAppClient; socket: FakeSocket; session: Session }> {
  const client = new SyncAppClient({ url: URL })
  client.setDeviceId(options.deviceId ?? 'web-test')

  const pending = client.connect({
    kind: 'password',
    username: 'alice',
    password: 'pw',
    register: false,
  })

  const socket = FakeSocket.last
  socket.open()
  replyToLast(socket, MsgType.WELCOME, { ...WELCOME, ...options.welcome })
  await tick()
  replyToLast(socket, MsgType.AUTH_OK, AUTH_OK)

  return { client, socket, session: await pending }
}

beforeEach(() => {
  FakeSocket.reset()
  vi.stubGlobal('WebSocket', FakeSocket)
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('handshake', () => {
  it('dials the configured url', () => {
    const client = new SyncAppClient({ url: URL })
    void client.connect({ kind: 'token', token: 't' }).catch(() => {})
    expect(FakeSocket.last.url).toBe(URL)
  })

  it('asks for binary frames', () => {
    // The protocol is binary; a socket left on "blob" would hand the read loop a
    // Blob instead of an ArrayBuffer and every frame would fail to parse.
    const client = new SyncAppClient({ url: URL })
    void client.connect({ kind: 'token', token: 't' }).catch(() => {})
    expect(FakeSocket.last.binaryType).toBe('arraybuffer')
  })

  it('sends HELLO first, before anything else', () => {
    // The gateway negotiates capabilities before it will accept auth. Sending
    // AUTH first is a protocol violation the server answers with an error.
    const client = new SyncAppClient({ url: URL })
    void client.connect({ kind: 'token', token: 't' }).catch(() => {})
    FakeSocket.last.open()

    expect(FakeSocket.last.frames[0].type).toBe(MsgType.HELLO)
  })

  it('advertises exactly the capabilities caps.ts declares', () => {
    const client = new SyncAppClient({ url: URL })
    client.setDeviceId('web-abc')
    void client.connect({ kind: 'token', token: 't' }).catch(() => {})
    FakeSocket.last.open()

    const hello = FakeSocket.last.frames[0].body as { caps: number; deviceId: string }
    expect(hello.caps).toBe(CLIENT_CAPS)
    expect(hello.deviceId).toBe('web-abc')
  })

  it('identifies the platform as web', () => {
    const client = new SyncAppClient({ url: URL })
    void client.connect({ kind: 'token', token: 't' }).catch(() => {})
    FakeSocket.last.open()

    expect((FakeSocket.last.frames[0].body as { platform: string }).platform).toBe('web')
  })

  it('correlates HELLO with a non-zero request id', () => {
    // RequestID 0 means "unsolicited push"; a request sent with 0 could never be
    // matched to its reply.
    const client = new SyncAppClient({ url: URL })
    void client.connect({ kind: 'token', token: 't' }).catch(() => {})
    FakeSocket.last.open()

    expect(FakeSocket.last.envelopes[0].requestId).not.toBe(0)
  })

  it('sends AUTH only after WELCOME arrives', async () => {
    const client = new SyncAppClient({ url: URL })
    const pending = client.connect({
      kind: 'password',
      username: 'alice',
      password: 'pw',
      register: true,
    })
    const socket = FakeSocket.last
    socket.open()

    expect(socket.lastOf(MsgType.AUTH)).toBeUndefined()

    replyToLast(socket, MsgType.WELCOME, WELCOME)
    await tick()

    expect(socket.lastOf(MsgType.AUTH)?.body).toMatchObject({
      username: 'alice',
      password: 'pw',
      register: true,
    })

    replyToLast(socket, MsgType.AUTH_OK, AUTH_OK)
    await pending
  })

  it('sends a token credential as a token, not as a username', async () => {
    const client = new SyncAppClient({ url: URL })
    const pending = client.connect({ kind: 'token', token: 'stored-token' })
    const socket = FakeSocket.last
    socket.open()
    replyToLast(socket, MsgType.WELCOME, WELCOME)
    await tick()

    expect(socket.lastOf(MsgType.AUTH)?.body).toMatchObject({ token: 'stored-token' })

    replyToLast(socket, MsgType.AUTH_OK, AUTH_OK)
    await pending
  })

  it('resolves with the session the gateway assigned', async () => {
    const { session } = await connected()
    expect(session).toMatchObject({
      userId: 'u1',
      sessionId: 's1',
      token: 'tok-1',
      resumeToken: 'resume-1',
      username: 'alice',
      displayName: 'Alice',
    })
  })

  it('exposes the session after connecting', async () => {
    const { client, session } = await connected()
    expect(client.currentSession).toEqual(session)
  })

  it('adopts the device id the gateway assigned when we sent none', async () => {
    // First launch has no stored device id. If we kept sending "", every
    // reconnect would look like a brand-new device to the server.
    const { client } = await connected({ deviceId: '' })
    const socket = FakeSocket.last
    socket.sent.length = 0

    client.send(MsgType.TYPING, { chatId: 'c1', userId: 'u1', active: true })
    expect(client.currentSession?.deviceId).toBe('dev-1')
  })
})

describe('connection state', () => {
  it('starts idle', () => {
    expect(new SyncAppClient({ url: URL }).state).toBe('idle')
  })

  it('walks idle → connecting → authenticating → ready', async () => {
    const seen: ConnectionState[] = []
    const client = new SyncAppClient({ url: URL })
    client.on('state', (state) => seen.push(state))

    const pending = client.connect({ kind: 'token', token: 't' })
    const socket = FakeSocket.last
    socket.open()
    replyToLast(socket, MsgType.WELCOME, WELCOME)
    await tick()
    replyToLast(socket, MsgType.AUTH_OK, AUTH_OK)
    await pending

    expect(seen).toEqual(['connecting', 'authenticating', 'ready'])
    expect(client.state).toBe('ready')
  })

  it('does not re-emit the same state twice', async () => {
    // The connection banner re-renders on every state event; a repeated 'ready'
    // would churn the whole tree for nothing.
    const seen: ConnectionState[] = []
    const { client, socket } = await connected()
    client.on('state', (state) => seen.push(state))

    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })
    expect(seen).toEqual([])
  })

  it('reports closed after close()', async () => {
    const { client } = await connected()
    client.close()
    expect(client.state).toBe('closed')
  })
})

describe('requests', () => {
  it('correlates a reply by request id', async () => {
    const { client, socket } = await connected()
    const pending = client.request<{ chatId: string }>(MsgType.CHAT_INFO, { chatId: 'c1' })

    const requestId = socket.envelopes.at(-1)!.requestId
    socket.deliver(MsgType.CHAT_INFO, { chatId: 'c1', title: 'General' }, { requestId })

    expect((await pending).body).toMatchObject({ chatId: 'c1', title: 'General' })
  })

  it('gives each request a distinct id so several can be in flight', async () => {
    // One socket, many concurrent requests — the id is the only thing keeping
    // their replies apart.
    const { client, socket } = await connected()
    const first = client.request(MsgType.CHAT_INFO, { chatId: 'c1' })
    const second = client.request(MsgType.CHAT_INFO, { chatId: 'c2' })

    const ids = socket.envelopes.slice(-2).map((envelope) => envelope.requestId)
    expect(ids[0]).not.toBe(ids[1])

    socket.deliver(MsgType.CHAT_INFO, { chatId: 'c2' }, { requestId: ids[1] })
    socket.deliver(MsgType.CHAT_INFO, { chatId: 'c1' }, { requestId: ids[0] })

    expect((await first).body).toMatchObject({ chatId: 'c1' })
    expect((await second).body).toMatchObject({ chatId: 'c2' })
  })

  it('resolves out-of-order replies to the right caller', async () => {
    const { client, socket } = await connected()
    const first = client.request<{ chatId: string }>(MsgType.CHAT_INFO, { chatId: 'c1' })
    const second = client.request<{ chatId: string }>(MsgType.CHAT_INFO, { chatId: 'c2' })
    const [idA, idB] = socket.envelopes.slice(-2).map((envelope) => envelope.requestId)

    socket.deliver(MsgType.CHAT_INFO, { chatId: 'c2' }, { requestId: idB })
    expect((await second).body.chatId).toBe('c2')

    socket.deliver(MsgType.CHAT_INFO, { chatId: 'c1' }, { requestId: idA })
    expect((await first).body.chatId).toBe('c1')
  })

  it('rejects with a ProtocolError when the gateway answers with ERROR', async () => {
    const { client, socket } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'nope' })
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(
      MsgType.ERROR,
      { code: ErrorCode.NOT_FOUND, message: 'no such chat', retryAfterMs: 0 },
      { requestId },
    )

    await expect(pending).rejects.toBeInstanceOf(ProtocolError)
  })

  it('carries the error code, message and retry hint through', async () => {
    // `retryAfterMs` is what the throttle path schedules on; dropping it would
    // make the client retry immediately and get throttled again.
    const { client, socket } = await connected()
    const pending = client.request(MsgType.SEND, { chatId: 'c1', dedupKey: 'd1', text: 'hi' })
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(
      MsgType.ERROR,
      { code: ErrorCode.RATE_LIMITED, message: 'slow down', retryAfterMs: 2500 },
      { requestId },
    )

    await expect(pending).rejects.toMatchObject({
      code: ErrorCode.RATE_LIMITED,
      message: 'slow down',
      retryAfterMs: 2500,
      class: 'throttle',
      retryable: true,
    })
  })

  it('refuses to send before the client is ready', () => {
    // Note the ready check throws synchronously rather than returning a rejected
    // promise: it is a caller bug, not a connection outcome, and a synchronous
    // throw surfaces it at the call site instead of as an unhandled rejection.
    const client = new SyncAppClient({ url: URL })
    expect(() => client.request(MsgType.CHAT_INFO, { chatId: 'c1' })).toThrow(/not ready/)
  })

  it('names the current state when refusing', () => {
    // "client not ready (state: reconnecting)" is diagnosable from a bug report;
    // a bare "not ready" is not.
    const client = new SyncAppClient({ url: URL })
    expect(() => client.request(MsgType.CHAT_INFO, {})).toThrow(/idle/)
  })

  it('times out rather than hanging forever', async () => {
    vi.useFakeTimers()
    const { client } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'c1' }, { timeoutMs: 5000 })
    const assertion = expect(pending).rejects.toThrow(/timed out/)

    await vi.advanceTimersByTimeAsync(5000)
    await assertion
  })

  it('names the request type in the timeout message', async () => {
    vi.useFakeTimers()
    const { client } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'c1' }, { timeoutMs: 1000 })
    const assertion = expect(pending).rejects.toThrow(/CHAT_INFO/)

    await vi.advanceTimersByTimeAsync(1000)
    await assertion
  })

  it('does not fire the timeout after a reply arrives', async () => {
    // A timer left armed would reject an already-resolved promise — harmless in
    // isolation, but it also leaks a handle per request for the full timeout.
    vi.useFakeTimers()
    const { client, socket } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'c1' }, { timeoutMs: 1000 })
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(MsgType.CHAT_INFO, { chatId: 'c1' }, { requestId })
    await pending
    await vi.advanceTimersByTimeAsync(5000)

    await expect(pending).resolves.toBeDefined()
  })
})

describe('streamed replies', () => {
  /**
   * HISTORY is the reason this path exists: the gateway replays stored messages
   * as ordinary NEW frames sharing the request id, then closes the page with
   * HISTORY_OK. Treating an item frame as the terminator would truncate history
   * to one message; treating the terminator as an item would hang the request.
   */
  it('collects item frames and resolves on the terminator', async () => {
    const { client, socket } = await connected()
    const pending = client.requestStream<{ text: string }, { done: boolean }>(
      MsgType.HISTORY,
      { chatId: 'c1', limit: 40 },
      { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK },
    )
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(MsgType.NEW, { messageId: 'm1', chatId: 'c1', text: 'one' }, { requestId })
    socket.deliver(MsgType.NEW, { messageId: 'm2', chatId: 'c1', text: 'two' }, { requestId })
    socket.deliver(
      MsgType.HISTORY_OK,
      { chatId: 'c1', nextBefore: 0, done: true },
      { requestId },
    )

    const { items, end } = await pending
    expect(items.map((item) => item.text)).toEqual(['one', 'two'])
    expect(end.done).toBe(true)
  })

  it('resolves with an empty page when the chat has no history', async () => {
    const { client, socket } = await connected()
    const pending = client.requestStream(
      MsgType.HISTORY,
      { chatId: 'c1' },
      { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK },
    )
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(MsgType.HISTORY_OK, { chatId: 'c1', done: true }, { requestId })
    expect((await pending).items).toEqual([])
  })

  it('does not swallow live fanout arriving mid-page', async () => {
    // Live NEW frames carry request id 0. If the stream collected those too, a
    // message sent while history loaded would vanish from the live path.
    const { client, socket } = await connected()
    const messages: unknown[] = []
    client.on('message', (message) => messages.push(message))

    const pending = client.requestStream<{ text: string }, unknown>(
      MsgType.HISTORY,
      { chatId: 'c1' },
      { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK },
    )
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(MsgType.NEW, { messageId: 'm1', chatId: 'c1', text: 'old' }, { requestId })
    socket.deliver(MsgType.NEW, { messageId: 'm2', chatId: 'c1', text: 'live' })
    socket.deliver(MsgType.HISTORY_OK, { chatId: 'c1', done: true }, { requestId })

    const { items } = await pending
    expect(items.map((item) => item.text)).toEqual(['old'])
    expect(messages).toHaveLength(1)
  })

  it('honours isTerminal when the terminator shares the item type', async () => {
    // CHAT_EXPORT streams every page — including the last — as the same type;
    // only a `done` flag separates them.
    const { client, socket } = await connected()
    const pending = client.requestStream<{ chatId: string }, { done: boolean }>(
      MsgType.CHAT_EXPORT,
      { chatId: 'c1' },
      {
        itemType: MsgType.CHAT_EXPORT_RESULT,
        endType: MsgType.CHAT_EXPORT_RESULT,
        isTerminal: (body) => (body as { done?: boolean }).done === true,
      },
    )
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(MsgType.CHAT_EXPORT_RESULT, { chatId: 'c1', done: false }, { requestId })
    socket.deliver(MsgType.CHAT_EXPORT_RESULT, { chatId: 'c1', done: false }, { requestId })
    socket.deliver(MsgType.CHAT_EXPORT_RESULT, { chatId: 'c1', done: true }, { requestId })

    const { items, end } = await pending
    expect(items).toHaveLength(2)
    expect(end.done).toBe(true)
  })

  it('rejects a streamed request that errors mid-page', async () => {
    const { client, socket } = await connected()
    const pending = client.requestStream(
      MsgType.HISTORY,
      { chatId: 'c1' },
      { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK },
    )
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(MsgType.NEW, { messageId: 'm1', chatId: 'c1' }, { requestId })
    socket.deliver(
      MsgType.ERROR,
      { code: ErrorCode.FORBIDDEN, message: 'not a member' },
      { requestId },
    )

    await expect(pending).rejects.toBeInstanceOf(ProtocolError)
  })

  it('times out a page that never terminates', async () => {
    vi.useFakeTimers()
    const { client, socket } = await connected()
    const pending = client.requestStream(
      MsgType.HISTORY,
      { chatId: 'c1' },
      { itemType: MsgType.NEW, endType: MsgType.HISTORY_OK, timeoutMs: 3000 },
    )
    const assertion = expect(pending).rejects.toThrow(/timed out/)
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliver(MsgType.NEW, { messageId: 'm1', chatId: 'c1' }, { requestId })
    await vi.advanceTimersByTimeAsync(3000)
    await assertion
  })
})

describe('unsolicited pushes', () => {
  it('emits a new message', async () => {
    const { client, socket } = await connected()
    const received: { text: string }[] = []
    client.on('message', (message) => received.push(message as { text: string }))

    socket.deliver(MsgType.NEW, { messageId: 'm1', chatId: 'c1', text: 'hello' })
    expect(received[0].text).toBe('hello')
  })

  it('routes each push type to its own event', async () => {
    const { client, socket } = await connected()
    const seen: string[] = []
    for (const event of [
      'message',
      'sendAck',
      'read',
      'typing',
      'presence',
      'reaction',
      'chatInfo',
      'pinned',
      'drafts',
      'profile',
      'delivered',
      'poll',
      'callState',
      'callSignal',
      'secret',
    ] as const) {
      client.on(event, () => seen.push(event))
    }

    socket.deliver(MsgType.NEW, { messageId: 'm1', chatId: 'c1' })
    socket.deliver(MsgType.SEND_ACK, { dedupKey: 'd1', messageId: 'm1', chatId: 'c1' })
    socket.deliver(MsgType.READ_UPD, { chatId: 'c1', userId: 'u2', upToChatSeq: 3 })
    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })
    socket.deliver(MsgType.PRESENCE, { userId: 'u2', online: true })
    socket.deliver(MsgType.REACT_UPD, { messageId: 'm1', chatId: 'c1' })
    socket.deliver(MsgType.CHAT_INFO, { chatId: 'c1' })
    socket.deliver(MsgType.PINNED, { chatId: 'c1' })
    socket.deliver(MsgType.DRAFTS, {})
    socket.deliver(MsgType.PROFILE, { userId: 'u1' })
    socket.deliver(MsgType.DELIVERED, { chatId: 'c1', userId: 'u2', upToChatSeq: 3 })
    socket.deliver(MsgType.POLL_STATE, { pollId: 'p1', chatId: 'c1' })
    socket.deliver(MsgType.CALL_STATE, { callId: 'call-1', chatId: 'c1' })
    socket.deliver(MsgType.CALL_SIGNAL, { callId: 'call-1' })
    socket.deliver(MsgType.SECRET_RECV, { chatId: 'c1' })

    expect(seen).toEqual([
      'message',
      'sendAck',
      'read',
      'typing',
      'presence',
      'reaction',
      'chatInfo',
      'pinned',
      'drafts',
      'profile',
      'delivered',
      'poll',
      'callState',
      'callSignal',
      'secret',
    ])
  })

  it('keeps DELIVERED separate from READ_UPD despite the shared body', async () => {
    // Both carry a cursor; conflating them would mark messages read the moment
    // they reached a device.
    const { client, socket } = await connected()
    const reads: unknown[] = []
    const delivers: unknown[] = []
    client.on('read', (payload) => reads.push(payload))
    client.on('delivered', (payload) => delivers.push(payload))

    socket.deliver(MsgType.DELIVERED, { chatId: 'c1', userId: 'u2', upToChatSeq: 5 })

    expect(delivers).toHaveLength(1)
    expect(reads).toHaveLength(0)
  })

  it('ignores an unknown message type instead of throwing', async () => {
    // The protocol's extensibility rule: a newer gateway may push types this
    // build has never heard of, and the connection must survive them.
    const { client, socket } = await connected()
    expect(() => socket.deliver(210, null)).not.toThrow()
    expect(client.state).toBe('ready')
  })

  it('answers a server PING with a PONG carrying the same request id', async () => {
    // The gateway drops a connection that stops answering its keepalive.
    const { socket } = await connected()
    socket.sent.length = 0

    socket.deliver(MsgType.PING, null, { requestId: 77 })

    const pong = socket.envelopes.at(-1)!
    expect(pong.type).toBe(MsgType.PONG)
    expect(pong.requestId).toBe(77)
  })

  it('ignores PONG and T_ACK without emitting anything', async () => {
    const { client, socket } = await connected()
    const errors: unknown[] = []
    client.on('error', (error) => errors.push(error))

    socket.deliver(MsgType.PONG, null)
    socket.deliver(MsgType.T_ACK, null)

    expect(errors).toEqual([])
    expect(client.state).toBe('ready')
  })

  it('emits an uncorrelated error on the error event', async () => {
    const { client, socket } = await connected()
    const errors: ProtocolError[] = []
    client.on('error', (error) => errors.push(error))

    socket.deliver(MsgType.ERROR, { code: ErrorCode.INTERNAL, message: 'boom' })

    expect(errors[0]).toBeInstanceOf(ProtocolError)
    expect(errors[0].code).toBe(ErrorCode.INTERNAL)
  })

  it('emits sessionExpired — not error — for an uncorrelated auth failure', async () => {
    // A session revoked from another device arrives out of the blue. The app has
    // to send the user back to login, which the generic error path does not do.
    const { client, socket } = await connected()
    const expired: ProtocolError[] = []
    const errors: unknown[] = []
    client.on('sessionExpired', (error) => expired.push(error))
    client.on('error', (error) => errors.push(error))

    socket.deliver(MsgType.ERROR, { code: ErrorCode.SESSION_REVOKED, message: 'revoked' })

    expect(expired).toHaveLength(1)
    expect(errors).toHaveLength(0)
  })

  it('stops reconnecting after the session is revoked', async () => {
    const { client, socket } = await connected()
    socket.deliver(MsgType.ERROR, { code: ErrorCode.SESSION_REVOKED, message: 'revoked' })

    const dialled = FakeSocket.instances.length
    socket.serverClose()
    await tick()

    expect(FakeSocket.instances.length).toBe(dialled)
    expect(client.state).toBe('closed')
  })
})

describe('event subscriptions', () => {
  it('delivers to every listener', async () => {
    const { client, socket } = await connected()
    const calls: string[] = []
    client.on('typing', () => calls.push('a'))
    client.on('typing', () => calls.push('b'))

    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })
    expect(calls).toEqual(['a', 'b'])
  })

  it('stops delivering after unsubscribe', async () => {
    // Every hook returns this from its effect cleanup; a leak here means a React
    // component keeps receiving events after unmount and calls setState on it.
    const { client, socket } = await connected()
    const calls: unknown[] = []
    const off = client.on('typing', (payload) => calls.push(payload))

    off()
    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })

    expect(calls).toEqual([])
  })

  it('unsubscribing one listener leaves the others', async () => {
    const { client, socket } = await connected()
    const calls: string[] = []
    const off = client.on('typing', () => calls.push('a'))
    client.on('typing', () => calls.push('b'))

    off()
    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true })

    expect(calls).toEqual(['b'])
  })

  it('isolates a throwing listener from the others', async () => {
    // One badly-behaved subscriber must not stop the rest of the app from seeing
    // a message — or, worse, kill the read loop for the whole connection.
    const { client, socket } = await connected()
    const calls: string[] = []
    client.on('typing', () => {
      throw new Error('listener bug')
    })
    client.on('typing', () => calls.push('survived'))

    expect(() =>
      socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true }),
    ).not.toThrow()
    expect(calls).toEqual(['survived'])
  })

  it('tolerates unsubscribing twice', async () => {
    const { client } = await connected()
    const off = client.on('typing', () => {})
    off()
    expect(() => off()).not.toThrow()
  })
})

describe('sequence and ack bookkeeping', () => {
  it('numbers outbound frames monotonically from 1', async () => {
    const { socket } = await connected()
    const seqs = socket.envelopes.map((envelope) => envelope.seq)
    expect(seqs).toEqual(seqs.map((_, index) => index + 1))
  })

  it('piggybacks the highest server seq seen as the ack', async () => {
    // This is what lets an idle client avoid explicit T_ACK frames; an ack that
    // never advances makes the gateway retain its replay buffer forever.
    const { client, socket } = await connected()
    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true }, { seq: 42 })

    client.send(MsgType.TYPING, { chatId: 'c1', userId: 'u1', active: true })
    expect(socket.envelopes.at(-1)!.ack).toBe(42)
  })

  it('never lets the ack go backwards on an out-of-order frame', async () => {
    const { client, socket } = await connected()
    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: true }, { seq: 42 })
    socket.deliver(MsgType.TYPING, { chatId: 'c1', userId: 'u2', active: false }, { seq: 7 })

    client.send(MsgType.TYPING, { chatId: 'c1', userId: 'u1', active: true })
    expect(socket.envelopes.at(-1)!.ack).toBe(42)
  })

  it('restarts the sequence at 1 on a new connection', async () => {
    // Seq is per-connection, not per-session — the gateway rejects a frame whose
    // seq does not start fresh after a redial.
    vi.useFakeTimers()
    const { socket } = await connected()
    socket.serverClose()

    await vi.advanceTimersByTimeAsync(60_000)
    const next = FakeSocket.last
    expect(next).not.toBe(socket)
    next.open()

    expect(next.envelopes[0].seq).toBe(1)
  })
})

describe('malformed input', () => {
  it('drops a frame with bad magic instead of tearing down the connection', async () => {
    // Anything can arrive on a public endpoint. A parse failure must cost one
    // frame, not the session.
    const { client, socket } = await connected()
    socket.deliverFrame(new Uint8Array([0x00, 0x00, 0x01, 0x00, 0, 0, 0, 0]))
    expect(client.state).toBe('ready')
  })

  it('drops a truncated frame', async () => {
    const { client, socket } = await connected()
    socket.deliverFrame(new Uint8Array([0x53, 0x43]))
    expect(client.state).toBe('ready')
  })

  it('drops a frame whose body does not match its declared type', async () => {
    const { client, socket } = await connected()
    socket.deliverFrame(encodeFrame(new Uint8Array([0xff, 0xff, 0xff, 0xff])))
    expect(client.state).toBe('ready')
  })

  /**
   * An undecodable *reply* used to leave its caller pending until the 15-second
   * timeout, which turns a stale `generated/` directory into a UI that hangs
   * rather than one that reports a problem.
   */
  it('fails the awaiting request when its reply cannot be decoded', async () => {
    const { client, socket } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'c1' })
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliverFrame(
      encodeFrame(
        // A CHAT_INFO envelope whose body is not valid protobuf.
        new Uint8Array([MsgType.CHAT_INFO, 1, 0, requestId, 4, 0xff, 0xff, 0xff, 0xff]),
      ),
    )

    await expect(pending).rejects.toMatchObject({ code: ErrorCode.BAD_FRAME })
  })

  it('keeps the connection usable after an undecodable reply', async () => {
    const { client, socket } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'c1' })
    const requestId = socket.envelopes.at(-1)!.requestId

    socket.deliverFrame(
      encodeFrame(
        new Uint8Array([MsgType.CHAT_INFO, 1, 0, requestId, 4, 0xff, 0xff, 0xff, 0xff]),
      ),
    )
    await expect(pending).rejects.toBeDefined()

    expect(client.state).toBe('ready')
    const second = client.request(MsgType.CHAT_INFO, { chatId: 'c2' })
    socket.deliver(
      MsgType.CHAT_INFO,
      { chatId: 'c2' },
      { requestId: socket.envelopes.at(-1)!.requestId },
    )
    await expect(second).resolves.toBeDefined()
  })
})

describe('reconnection', () => {
  it('redials after an unexpected drop', async () => {
    vi.useFakeTimers()
    const { socket } = await connected()
    const before = FakeSocket.instances.length

    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)

    expect(FakeSocket.instances.length).toBeGreaterThan(before)
  })

  it('reports reconnecting while it waits', async () => {
    vi.useFakeTimers()
    const { client, socket } = await connected()
    socket.serverClose()
    await tick()
    expect(client.state).toBe('reconnecting')
  })

  it('rejects in-flight requests on a drop rather than leaving them hanging', async () => {
    // The replies are gone with the socket; waiting out the 15s timeout would
    // leave spinners on screen for a reconnect that takes one second.
    vi.useFakeTimers()
    const { client, socket } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'c1' })
    const assertion = expect(pending).rejects.toBeDefined()

    socket.serverClose()
    await vi.advanceTimersByTimeAsync(1)
    await assertion
  })

  it('resumes with the stored token instead of re-authenticating', async () => {
    // RESUME replays what the gateway buffered while we were away; a full AUTH
    // starts a new session and loses that buffer.
    vi.useFakeTimers()
    const { socket } = await connected()
    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)

    const next = FakeSocket.last
    next.open()
    replyToLast(next, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)

    expect(next.lastOf(MsgType.RESUME)?.body).toMatchObject({ resumeToken: 'resume-1' })
    expect(next.lastOf(MsgType.AUTH)).toBeUndefined()
  })

  it('uses the ROTATED resume token on the next reconnect', async () => {
    /*
     * The regression this guards is silent and self-inflicted.
     *
     * Resuming CONSUMES the token that was sent, and the server remembers the
     * consumed one so that presenting it again is DETECTABLE — it treats that as
     * theft (two parties, one token, no way to tell which is the owner) and kills
     * the chain. So a client that keeps its old token works for exactly one
     * reconnect and then logs itself out, having done nothing wrong.
     */
    vi.useFakeTimers()
    const { socket } = await connected()

    // First reconnect: resume with the original token, and the server hands back a
    // new one.
    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)
    const second = FakeSocket.last
    second.open()
    replyToLast(second, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)

    expect(second.lastOf(MsgType.RESUME)?.body).toMatchObject({ resumeToken: 'resume-1' })
    const resumeId = second.envelopes.at(-1)!.requestId
    second.deliver(
      MsgType.RESUME_OK,
      { sessionId: 'sess-1', fromSeq: 0, resumeToken: 'resume-2' },
      { requestId: resumeId },
    )
    await vi.advanceTimersByTimeAsync(1)

    // Second reconnect: the NEW token, not the one already spent.
    second.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)
    const third = FakeSocket.last
    third.open()
    replyToLast(third, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)

    expect(third.lastOf(MsgType.RESUME)?.body).toMatchObject({ resumeToken: 'resume-2' })
  })

  it('keeps the existing token when the gateway sends no rotated one', async () => {
    // An older gateway does not rotate, so RESUME_OK carries no token. Overwriting
    // unconditionally would blank the one we have and force a full re-auth on every
    // reconnect after this.
    vi.useFakeTimers()
    const { socket } = await connected()

    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)
    const second = FakeSocket.last
    second.open()
    replyToLast(second, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)
    const resumeId = second.envelopes.at(-1)!.requestId
    second.deliver(
      MsgType.RESUME_OK,
      { sessionId: 'sess-1', fromSeq: 0 },
      { requestId: resumeId },
    )
    await vi.advanceTimersByTimeAsync(1)

    second.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)
    const third = FakeSocket.last
    third.open()
    replyToLast(third, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)

    expect(third.lastOf(MsgType.RESUME)?.body).toMatchObject({ resumeToken: 'resume-1' })
  })

  it('falls back to a full AUTH when the gateway refuses the resume', async () => {
    // An expired replay buffer is recoverable — we still hold a bearer token.
    vi.useFakeTimers()
    const { socket } = await connected()
    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)

    const next = FakeSocket.last
    next.open()
    replyToLast(next, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)

    const resumeId = next.envelopes.at(-1)!.requestId
    next.deliver(
      MsgType.ERROR,
      { code: ErrorCode.RESUME_EXPIRED, message: 'buffer gone' },
      { requestId: resumeId },
    )
    await vi.advanceTimersByTimeAsync(1)

    expect(next.lastOf(MsgType.AUTH)?.body).toMatchObject({ token: 'tok-1' })
  })

  it('does not attempt a resume the gateway says it cannot honour', async () => {
    vi.useFakeTimers()
    const { socket } = await connected()
    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)

    const next = FakeSocket.last
    next.open()
    replyToLast(next, MsgType.WELCOME, { ...WELCOME, resumeSupported: false })
    await vi.advanceTimersByTimeAsync(1)

    expect(next.lastOf(MsgType.RESUME)).toBeUndefined()
    expect(next.lastOf(MsgType.AUTH)).toBeDefined()
  })

  it('does not attempt a resume when the capability was not negotiated', async () => {
    vi.useFakeTimers()
    const { socket } = await connected()
    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)

    const next = FakeSocket.last
    next.open()
    replyToLast(next, MsgType.WELCOME, { ...WELCOME, caps: CLIENT_CAPS & ~Cap.RESUME })
    await vi.advanceTimersByTimeAsync(1)

    expect(next.lastOf(MsgType.RESUME)).toBeUndefined()
  })

  it('prefers the bearer token over the password on a redial', async () => {
    // The password should not sit in memory for the life of the tab, and a token
    // keeps working across a password change elsewhere.
    vi.useFakeTimers()
    const { socket } = await connected()
    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)

    const next = FakeSocket.last
    next.open()
    replyToLast(next, MsgType.WELCOME, { ...WELCOME, resumeSupported: false })
    await vi.advanceTimersByTimeAsync(1)

    const auth = next.lastOf(MsgType.AUTH)?.body as { token: string; password: string }
    expect(auth.token).toBe('tok-1')
    expect(auth.password).toBe('')
  })

  it('backs off further with each failed attempt', async () => {
    // Without backoff, a gateway restart turns every client into a tight redial
    // loop and the fleet cannot come back up.
    vi.useFakeTimers()
    const { socket } = await connected()
    socket.serverClose()
    await vi.advanceTimersByTimeAsync(2000)
    const afterFirst = FakeSocket.instances.length

    FakeSocket.last.serverClose()
    await vi.advanceTimersByTimeAsync(1000)

    expect(FakeSocket.instances.length).toBe(afterFirst)

    await vi.advanceTimersByTimeAsync(60_000)
    expect(FakeSocket.instances.length).toBeGreaterThan(afterFirst)
  })

  it('stops reconnecting after close()', async () => {
    vi.useFakeTimers()
    const { client, socket } = await connected()
    client.close()
    socket.serverClose()

    const dialled = FakeSocket.instances.length
    await vi.advanceTimersByTimeAsync(60_000)

    expect(FakeSocket.instances.length).toBe(dialled)
  })

  it('gives up for good on an auth failure and reports the session as expired', async () => {
    // Retrying a bad token just produces the same rejection; the user has to log
    // in again, and only the app can ask them to.
    vi.useFakeTimers()
    const { client, socket } = await connected()
    const expired: ProtocolError[] = []
    client.on('sessionExpired', (error) => expired.push(error))

    socket.serverClose()
    await vi.advanceTimersByTimeAsync(60_000)

    const next = FakeSocket.last
    next.open()
    replyToLast(next, MsgType.WELCOME, { ...WELCOME, resumeSupported: false })
    await vi.advanceTimersByTimeAsync(1)

    next.deliver(
      MsgType.AUTH_ERR,
      { code: ErrorCode.BAD_TOKEN, message: 'expired' },
      { requestId: next.envelopes.at(-1)!.requestId },
    )
    await vi.advanceTimersByTimeAsync(60_000)

    expect(expired).toHaveLength(1)
    expect(client.state).toBe('closed')
  })

  it('forces a redial when the gateway goes silent past the liveness timeout', async () => {
    // A suspended laptop or a dead mobile link leaves a socket that looks open
    // but never delivers again. The browser will not tell us; the timer does.
    vi.useFakeTimers()
    const client = new SyncAppClient({ url: URL, livenessTimeoutMs: 5000 })
    const pending = client.connect({ kind: 'token', token: 't' })
    const socket = FakeSocket.last
    socket.open()
    replyToLast(socket, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)
    replyToLast(socket, MsgType.AUTH_OK, AUTH_OK)
    await pending

    const before = FakeSocket.instances.length
    await vi.advanceTimersByTimeAsync(5000)
    await vi.advanceTimersByTimeAsync(60_000)

    expect(FakeSocket.instances.length).toBeGreaterThan(before)
  })

  it('rearms liveness on every inbound frame', async () => {
    // Any traffic proves the link is alive; only genuine silence should redial.
    vi.useFakeTimers()
    const client = new SyncAppClient({ url: URL, livenessTimeoutMs: 5000 })
    const pending = client.connect({ kind: 'token', token: 't' })
    const socket = FakeSocket.last
    socket.open()
    replyToLast(socket, MsgType.WELCOME, WELCOME)
    await vi.advanceTimersByTimeAsync(1)
    replyToLast(socket, MsgType.AUTH_OK, AUTH_OK)
    await pending

    const before = FakeSocket.instances.length
    for (let i = 0; i < 5; i++) {
      await vi.advanceTimersByTimeAsync(4000)
      socket.deliver(MsgType.PING, null, { requestId: 0 })
    }

    expect(FakeSocket.instances.length).toBe(before)
  })
})

describe('first connect failure', () => {
  it('rejects rather than retrying silently when the very first dial drops', async () => {
    // Someone is awaiting connect(); a background retry would leave the login
    // form spinning with no error to show.
    const client = new SyncAppClient({ url: URL })
    const pending = client.connect({ kind: 'token', token: 't' })
    const assertion = expect(pending).rejects.toBeDefined()

    FakeSocket.last.serverClose()
    await assertion
    expect(client.state).toBe('closed')
  })

  it('rejects with the auth error when the gateway refuses the credentials', async () => {
    const client = new SyncAppClient({ url: URL })
    const pending = client.connect({
      kind: 'password',
      username: 'alice',
      password: 'wrong',
      register: false,
    })
    const assertion = expect(pending).rejects.toMatchObject({ code: ErrorCode.UNAUTHENTICATED })

    const socket = FakeSocket.last
    socket.open()
    replyToLast(socket, MsgType.WELCOME, WELCOME)
    await tick()
    replyToLast(socket, MsgType.AUTH_ERR, {
      code: ErrorCode.UNAUTHENTICATED,
      message: 'bad credentials',
    })

    await assertion
  })

  it('reports a constructor failure instead of throwing synchronously', async () => {
    // A blocked or malformed URL makes `new WebSocket()` throw; connect() must
    // still return a promise so callers have one error path.
    class Exploding {
      constructor() {
        throw new Error('SecurityError')
      }
    }
    vi.stubGlobal('WebSocket', Exploding)

    const client = new SyncAppClient({ url: 'ws://blocked' })
    await expect(client.connect({ kind: 'token', token: 't' })).rejects.toThrow(/SecurityError/)
  })
})

describe('close', () => {
  it('rejects everything in flight', async () => {
    const { client } = await connected()
    const pending = client.request(MsgType.CHAT_INFO, { chatId: 'c1' })
    const assertion = expect(pending).rejects.toThrow(/closed/)

    client.close()
    await assertion
  })

  it('forgets the session', async () => {
    // Logout has to leave nothing behind for the next user of this tab.
    const { client } = await connected()
    client.close()
    expect(client.currentSession).toBeNull()
  })

  it('refuses further sends', async () => {
    const { client } = await connected()
    client.close()
    expect(() =>
      client.send(MsgType.TYPING, { chatId: 'c1', userId: 'u1', active: true }),
    ).toThrow(/not ready/)
  })

  it('is safe to call twice', async () => {
    const { client } = await connected()
    client.close()
    expect(() => client.close()).not.toThrow()
  })

  it('leaves no timer able to redial', async () => {
    vi.useFakeTimers()
    const { client } = await connected()
    const dialled = FakeSocket.instances.length

    client.close()
    await vi.advanceTimersByTimeAsync(120_000)

    expect(FakeSocket.instances.length).toBe(dialled)
  })
})

describe('fire-and-forget send', () => {
  it('writes with request id 0', async () => {
    // The gateway answers these only on failure, and an uncorrelated reply is
    // exactly what request id 0 means.
    const { client, socket } = await connected()
    socket.sent.length = 0

    client.send(MsgType.TYPING, { chatId: 'c1', userId: 'u1', active: true })
    expect(socket.envelopes.at(-1)!.requestId).toBe(0)
  })

  it('refuses before the client is ready', () => {
    const client = new SyncAppClient({ url: URL })
    expect(() => client.send(MsgType.TYPING, { chatId: 'c1' })).toThrow(/not ready/)
  })

  it('round-trips the body through the wire codec', async () => {
    const { client, socket } = await connected()
    client.send(MsgType.READ, { chatId: 'c1', upToChatSeq: 12 })
    expect(socket.lastOf(MsgType.READ)?.body).toMatchObject({ chatId: 'c1', upToChatSeq: 12 })
  })
})
