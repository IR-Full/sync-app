/**
 * SynapseClient — the whole custom-protocol surface, in one place.
 *
 * The gateway speaks a binary protocol over a WebSocket (`/ws`), not REST: one
 * binary WS message == one frame == one envelope. Everything the app needs —
 * auth, sending, history, receipts, typing — rides this single connection, so
 * this class owns the connection lifecycle end to end:
 *
 *   dial -> HELLO/WELCOME (capability negotiation)
 *        -> AUTH or RESUME (identity)
 *        -> steady state: request/reply by RequestID + unsolicited pushes
 *        -> drop -> backoff -> redial, resuming the session when possible
 *
 * Deliberately transport-only. It knows nothing about React, TanStack Query or
 * chats-as-a-concept; higher layers subscribe to its events. That keeps the
 * protocol quirks (seq/ack bookkeeping, the streamed-history convention,
 * request correlation) from leaking into components.
 */
import { CLIENT_CAPS, hasCap, Cap } from './caps'
import { decodeBody, encodeBody, hasBody } from './codec'
import { decodeEnvelope, encodeEnvelope, type Envelope } from './envelope'
import { ErrorCode, isAuthError, ProtocolError } from './error-code'
import { decodeFrame, encodeFrame } from './frame'
import { MsgType, msgTypeName } from './msg-type'
import type * as Body from './generated/bodies'

export type ConnectionState =
  'idle' | 'connecting' | 'authenticating' | 'ready' | 'reconnecting' | 'closed'

export type Credentials =
  | { kind: 'token'; token: string }
  | { kind: 'password'; username: string; password: string; register: boolean }

export interface Session {
  userId: string
  deviceId: string
  sessionId: string
  token: string
  resumeToken: string
}

/** Unsolicited server pushes, plus connection-level signals. */
export interface ClientEvents {
  state: ConnectionState
  message: Body.NewMessage
  sendAck: Body.SendAck
  read: Body.ReadUpdate
  typing: Body.Typing
  presence: Body.Presence
  reaction: Body.ReactUpdate
  chatInfo: Body.ChatInfo
  /** a chat's pin set changed (chat-wide, every member gets it) */
  pinned: Body.Pinned
  /** draft mirrored from another device of this same user (private) */
  drafts: Body.Drafts
  /** poll created, voted on, or closed */
  poll: Body.PollState
  /** call room lifecycle + roster */
  callState: Body.CallState
  /** opaque SDP/ICE relayed between devices */
  callSignal: Body.CallSignal
  /** ciphertext delivered from a peer device in a secret chat */
  secret: Body.SecretMsg
  /** an error frame that did not correlate to any in-flight request */
  error: ProtocolError
  /** the session is no longer valid — the app must send the user back to login */
  sessionExpired: ProtocolError
}

type Listener<K extends keyof ClientEvents> = (payload: ClientEvents[K]) => void

interface Pending {
  resolve: (envelope: DecodedEnvelope) => void
  reject: (error: unknown) => void
  timer: ReturnType<typeof setTimeout>
  /** collects streamed items when the reply is a page (HISTORY, THREAD, export) */
  stream?: {
    itemType: number
    endType: number
    items: unknown[]
    /**
     * Decides whether a frame of `itemType` is actually the terminator. Needed
     * for CHAT_EXPORT, where every page — including the last — is a
     * CHAT_EXPORT_RESULT and only a `done` flag separates them.
     */
    isTerminal?: (body: unknown) => boolean
  }
}

export interface DecodedEnvelope<T = unknown> {
  type: number
  seq: number
  requestId: number
  body: T
}

export interface SynapseClientOptions {
  url: string
  clientVersion?: string
  /** how long a request may wait for its reply before rejecting */
  requestTimeoutMs?: number
  /** ceiling for reconnect backoff */
  maxBackoffMs?: number
  /** force a reconnect if the server has been silent this long (it pings every 20s) */
  livenessTimeoutMs?: number
}

const DEFAULTS = {
  clientVersion: 'web/0.1',
  requestTimeoutMs: 15_000,
  maxBackoffMs: 30_000,
  livenessTimeoutMs: 45_000,
}

export class SynapseClient {
  private readonly options: Required<SynapseClientOptions>
  private socket: WebSocket | null = null
  private _state: ConnectionState = 'idle'

  /** our per-connection monotonic sequence (envelope.Seq) */
  private seq = 0
  /** highest server Seq observed — piggybacked as envelope.Ack on everything we send */
  private ackSeq = 0
  private requestCounter = 0
  private readonly pending = new Map<number, Pending>()
  private readonly listeners = new Map<keyof ClientEvents, Set<Listener<never>>>()

  private credentials: Credentials | null = null
  private session: Session | null = null
  private deviceId = ''
  private negotiatedCaps = 0
  private heartbeatMs = 20_000

  private reconnectAttempts = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private livenessTimer: ReturnType<typeof setTimeout> | null = null
  private shouldReconnect = false
  /** resolves/rejects the in-flight connect() call */
  private connectDeferred: {
    resolve: (session: Session) => void
    reject: (error: unknown) => void
  } | null = null

  constructor(options: SynapseClientOptions) {
    this.options = { ...DEFAULTS, ...options }
  }

  get state(): ConnectionState {
    return this._state
  }

  get currentSession(): Session | null {
    return this.session
  }

  /** Stable per-installation device id; the server keys sessions and multi-device delivery on it. */
  setDeviceId(deviceId: string): void {
    this.deviceId = deviceId
  }

  // ---------------------------------------------------------------- events

  on<K extends keyof ClientEvents>(event: K, listener: Listener<K>): () => void {
    let set = this.listeners.get(event)
    if (!set) {
      set = new Set()
      this.listeners.set(event, set)
    }
    set.add(listener as Listener<never>)
    return () => {
      set!.delete(listener as Listener<never>)
    }
  }

  private emit<K extends keyof ClientEvents>(event: K, payload: ClientEvents[K]): void {
    const set = this.listeners.get(event)
    if (!set) return
    for (const listener of set) {
      try {
        ;(listener as Listener<K>)(payload)
      } catch (error) {
        console.error(`[synapse] listener for "${event}" threw`, error)
      }
    }
  }

  private setState(state: ConnectionState): void {
    if (this._state === state) return
    this._state = state
    this.emit('state', state)
  }

  // ------------------------------------------------------------- lifecycle

  /**
   * Opens the connection and authenticates. Resolves with the session once the
   * gateway has accepted us, so callers can await "actually usable", not merely
   * "socket open".
   */
  connect(credentials: Credentials): Promise<Session> {
    this.credentials = credentials
    this.shouldReconnect = true
    this.reconnectAttempts = 0
    return new Promise<Session>((resolve, reject) => {
      this.connectDeferred = { resolve, reject }
      this.dial()
    })
  }

  /** Closes for good: no reconnect, all in-flight requests rejected. */
  close(): void {
    this.shouldReconnect = false
    this.clearTimers()
    this.rejectAllPending(new Error('client closed'))
    this.socket?.close()
    this.socket = null
    this.session = null
    this.credentials = null
    this.setState('closed')
  }

  private dial(): void {
    this.setState(this.reconnectAttempts > 0 ? 'reconnecting' : 'connecting')
    this.seq = 0
    this.ackSeq = 0

    let socket: WebSocket
    try {
      socket = new WebSocket(this.options.url)
    } catch (error) {
      this.handleDisconnect(error)
      return
    }
    socket.binaryType = 'arraybuffer'
    this.socket = socket

    socket.onopen = () => {
      void this.handshakeAndAuth().catch((error) => {
        // A rejected handshake/auth is terminal for this socket; handleDisconnect
        // decides whether it is terminal for the client too.
        socket.close()
        this.handleDisconnect(error)
      })
    }
    socket.onmessage = (event) => this.handleFrame(event.data as ArrayBuffer)
    socket.onerror = () => {
      // The error event carries no useful detail in browsers; onclose follows.
    }
    socket.onclose = () => {
      if (this.socket === socket) this.handleDisconnect(new Error('connection closed'))
    }
  }

  private async handshakeAndAuth(): Promise<void> {
    this.setState('authenticating')

    // --- HELLO / WELCOME: negotiate capabilities before anything else.
    const welcome = await this.request<Body.Welcome>(
      MsgType.HELLO,
      {
        clientVersion: this.options.clientVersion,
        deviceId: this.deviceId,
        platform: 'web',
        caps: CLIENT_CAPS,
        resumeToken: '',
      } satisfies Body.Encodable<Body.Hello>,
      { expect: MsgType.WELCOME, skipReadyCheck: true },
    )
    this.negotiatedCaps = welcome.body.caps
    if (welcome.body.heartbeatMs > 0) this.heartbeatMs = welcome.body.heartbeatMs

    // --- AUTH or RESUME. The gateway accepts exactly one of these as the next
    // frame, so the choice is made here rather than opportunistically later.
    const canResume =
      this.session?.resumeToken !== undefined &&
      this.session.resumeToken !== '' &&
      welcome.body.resumeSupported &&
      hasCap(this.negotiatedCaps, Cap.RESUME)

    if (canResume) {
      try {
        await this.resumeSession()
        return
      } catch (error) {
        // A refused resume (expired buffer, revoked session) is recoverable: fall
        // through to a full re-auth with the bearer token we still hold.
        if (error instanceof ProtocolError && isAuthError(error.code)) throw error
      }
      // The socket is still usable — the gateway only errors, it does not close —
      // but it is now waiting for a fresh AUTH.
    }

    await this.authenticate()
  }

  private async resumeSession(): Promise<void> {
    const session = this.session!
    await this.request<Body.ResumeOK>(
      MsgType.RESUME,
      {
        resumeToken: session.resumeToken,
        lastAckSeq: this.ackSeq,
      } satisfies Body.Encodable<Body.Resume>,
      { expect: MsgType.RESUME_OK, skipReadyCheck: true },
    )
    this.onAuthenticated(session)
  }

  private async authenticate(): Promise<void> {
    const credentials = this.credentials
    if (!credentials) throw new Error('no credentials')

    const body: Body.Encodable<Body.Auth> =
      credentials.kind === 'token'
        ? { token: credentials.token }
        : {
            username: credentials.username,
            password: credentials.password,
            register: credentials.register,
          }

    const reply = await this.request<Body.AuthOK>(MsgType.AUTH, body, {
      expect: MsgType.AUTH_OK,
      skipReadyCheck: true,
    })

    const session: Session = {
      userId: reply.body.userId,
      deviceId: reply.body.deviceId,
      sessionId: reply.body.sessionId,
      token: reply.body.token,
      resumeToken: reply.body.resumeToken,
    }
    // The gateway assigns a device id when we sent none; adopt it so a later
    // reconnect presents the same device.
    if (session.deviceId) this.deviceId = session.deviceId
    this.onAuthenticated(session)
  }

  private onAuthenticated(session: Session): void {
    this.session = session
    this.reconnectAttempts = 0
    this.setState('ready')
    this.armLiveness()
    // Once authenticated, a token beats a password for any future reconnect:
    // it survives a password change and avoids holding credentials in memory.
    if (session.token) this.credentials = { kind: 'token', token: session.token }
    this.connectDeferred?.resolve(session)
    this.connectDeferred = null
  }

  private handleDisconnect(error: unknown): void {
    this.clearTimers()
    this.socket = null
    this.rejectAllPending(error)

    if (!this.shouldReconnect) {
      this.setState('closed')
      return
    }

    // An auth-class failure means retrying cannot help: surface it and stop.
    if (error instanceof ProtocolError && isAuthError(error.code)) {
      this.shouldReconnect = false
      this.setState('closed')
      this.connectDeferred?.reject(error)
      this.connectDeferred = null
      this.emit('sessionExpired', error)
      return
    }

    // The very first connect fails loudly (there is a caller awaiting it);
    // later drops retry silently in the background.
    if (this.connectDeferred && this.reconnectAttempts === 0 && !this.session) {
      this.shouldReconnect = false
      this.setState('closed')
      this.connectDeferred.reject(error)
      this.connectDeferred = null
      return
    }

    this.setState('reconnecting')
    const delay = this.backoffDelay()
    this.reconnectAttempts++
    this.reconnectTimer = setTimeout(() => this.dial(), delay)
  }

  /** Exponential backoff with full jitter — spreads a fleet-wide reconnect storm. */
  private backoffDelay(): number {
    const base = Math.min(1000 * 2 ** this.reconnectAttempts, this.options.maxBackoffMs)
    return Math.round(base / 2 + Math.random() * (base / 2))
  }

  private armLiveness(): void {
    if (this.livenessTimer) clearTimeout(this.livenessTimer)
    this.livenessTimer = setTimeout(() => {
      // The gateway pings every heartbeat interval; prolonged silence means the
      // socket is dead in a way the browser has not noticed (common on suspended
      // laptops and flaky mobile links). Force the cycle rather than wait.
      this.socket?.close()
    }, this.options.livenessTimeoutMs)
  }

  private clearTimers(): void {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer)
    if (this.livenessTimer) clearTimeout(this.livenessTimer)
    this.reconnectTimer = null
    this.livenessTimer = null
  }

  private rejectAllPending(error: unknown): void {
    for (const [, pending] of this.pending) {
      clearTimeout(pending.timer)
      pending.reject(error)
    }
    this.pending.clear()
  }

  // ------------------------------------------------------------ inbound

  private handleFrame(data: ArrayBuffer): void {
    this.armLiveness()
    let envelope: Envelope
    try {
      envelope = decodeEnvelope(decodeFrame(new Uint8Array(data)))
    } catch (error) {
      console.error('[synapse] dropping malformed frame', error)
      return
    }

    // Track the highest server sequence for the piggybacked ack (and for RESUME).
    if (envelope.seq > this.ackSeq) this.ackSeq = envelope.seq

    if (envelope.type === MsgType.PING) {
      this.write(MsgType.PONG, null, envelope.requestId)
      return
    }
    if (envelope.type === MsgType.PONG || envelope.type === MsgType.T_ACK) return

    // Decode even a zero-length body: a protobuf message whose fields are all
    // at their defaults encodes to no bytes at all, and the gateway sends
    // exactly that for "empty" replies (an empty schedule list, a revoked
    // invite, a push-token ack). Treating those as `null` would make a
    // legitimate answer look like a missing one.
    let body: unknown = null
    if (hasBody(envelope.type)) {
      try {
        body = decodeBody(envelope.type, envelope.body)
      } catch (error) {
        console.error(`[synapse] cannot decode ${msgTypeName(envelope.type)} body`, error)
        return
      }
    }

    const decoded: DecodedEnvelope = {
      type: envelope.type,
      seq: envelope.seq,
      requestId: envelope.requestId,
      body,
    }

    if (envelope.requestId !== 0 && this.pending.has(envelope.requestId)) {
      this.settle(decoded)
      return
    }
    this.dispatchPush(decoded)
  }

  /** Routes a correlated reply to its awaiting request, accumulating streamed pages. */
  private settle(envelope: DecodedEnvelope): void {
    const pending = this.pending.get(envelope.requestId)!

    if (envelope.type === MsgType.ERROR || envelope.type === MsgType.AUTH_ERR) {
      const error = envelope.body as Body.Error | null
      this.pending.delete(envelope.requestId)
      clearTimeout(pending.timer)
      pending.reject(
        new ProtocolError(
          error?.code ?? ErrorCode.INTERNAL,
          error?.message ?? 'unknown error',
          error?.retryAfterMs ?? 0,
        ),
      )
      return
    }

    // Paged replies (HISTORY, THREAD, CHAT_EXPORT) stream N item frames sharing
    // our RequestID, then a terminator carrying the cursor. Keep the request
    // open until the terminator lands.
    if (
      pending.stream &&
      envelope.type === pending.stream.itemType &&
      !pending.stream.isTerminal?.(envelope.body)
    ) {
      pending.stream.items.push(envelope.body)
      return
    }

    this.pending.delete(envelope.requestId)
    clearTimeout(pending.timer)
    pending.resolve(envelope)
  }

  private dispatchPush(envelope: DecodedEnvelope): void {
    switch (envelope.type) {
      case MsgType.NEW:
        this.emit('message', envelope.body as Body.NewMessage)
        break
      case MsgType.SEND_ACK:
        this.emit('sendAck', envelope.body as Body.SendAck)
        break
      case MsgType.READ_UPD:
        this.emit('read', envelope.body as Body.ReadUpdate)
        break
      case MsgType.TYPING:
        this.emit('typing', envelope.body as Body.Typing)
        break
      case MsgType.PRESENCE:
        this.emit('presence', envelope.body as Body.Presence)
        break
      case MsgType.REACT_UPD:
        this.emit('reaction', envelope.body as Body.ReactUpdate)
        break
      case MsgType.CHAT_INFO:
        this.emit('chatInfo', envelope.body as Body.ChatInfo)
        break
      case MsgType.PINNED:
        this.emit('pinned', envelope.body as Body.Pinned)
        break
      case MsgType.DRAFTS:
        this.emit('drafts', envelope.body as Body.Drafts)
        break
      case MsgType.POLL_STATE:
        this.emit('poll', envelope.body as Body.PollState)
        break
      case MsgType.CALL_STATE:
        this.emit('callState', envelope.body as Body.CallState)
        break
      case MsgType.CALL_SIGNAL:
        this.emit('callSignal', envelope.body as Body.CallSignal)
        break
      case MsgType.SECRET_RECV:
        this.emit('secret', envelope.body as Body.SecretMsg)
        break
      case MsgType.ERROR:
      case MsgType.AUTH_ERR: {
        const raw = envelope.body as Body.Error | null
        const error = new ProtocolError(
          raw?.code ?? ErrorCode.INTERNAL,
          raw?.message ?? 'unknown error',
          raw?.retryAfterMs ?? 0,
        )
        // An uncorrelated auth error means the session died underneath us
        // (revoked elsewhere, expired) — the app has to react, not just log.
        if (isAuthError(error.code)) {
          this.shouldReconnect = false
          this.emit('sessionExpired', error)
        } else {
          this.emit('error', error)
        }
        break
      }
      default:
        // Unknown types are ignored by design: the protocol's extensibility rule
        // is that a client which does not understand a type must skip it.
        break
    }
  }

  // ------------------------------------------------------------- outbound

  private write(type: number, body: object | null, requestId = 0): void {
    const socket = this.socket
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      throw new Error('not connected')
    }
    const envelope: Envelope = {
      type,
      seq: ++this.seq,
      ack: this.ackSeq,
      requestId,
      body: encodeBody(type, body),
    }
    socket.send(encodeFrame(encodeEnvelope(envelope)))
  }

  /** Fire-and-forget. Used for types the gateway answers only on failure (READ, TYPING, EDIT, DELETE). */
  send(type: number, body: object | null): void {
    this.assertReady()
    this.write(type, body)
  }

  /**
   * Sends a request and waits for its correlated reply.
   *
   * `expect` is the happy-path reply type; anything else that correlates (other
   * than a streamed item) resolves too, so callers can handle multi-shaped
   * replies, while ERROR frames reject with a ProtocolError.
   */
  request<T>(
    type: number,
    body: object | null,
    options: { expect?: number; skipReadyCheck?: boolean; timeoutMs?: number } = {},
  ): Promise<DecodedEnvelope<T>> {
    if (!options.skipReadyCheck) this.assertReady()
    const requestId = ++this.requestCounter

    return new Promise<DecodedEnvelope<T>>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(requestId)
        reject(new Error(`request ${msgTypeName(type)} timed out`))
      }, options.timeoutMs ?? this.options.requestTimeoutMs)

      this.pending.set(requestId, {
        resolve: resolve as (envelope: DecodedEnvelope) => void,
        reject,
        timer,
      })

      try {
        this.write(type, body, requestId)
      } catch (error) {
        clearTimeout(timer)
        this.pending.delete(requestId)
        reject(error)
      }
    })
  }

  /**
   * Sends a request whose reply is a page: N frames of `itemType` followed by a
   * terminator of `endType`, all sharing our RequestID.
   *
   * This is how HISTORY works — the gateway replays stored messages as ordinary
   * NEW frames so the client's normal ingest path handles them, then closes the
   * page with HISTORY_OK carrying the next cursor. The shared RequestID is the
   * only thing distinguishing a backfilled message from live fanout (which
   * always carries RequestID 0).
   */
  requestStream<TItem, TEnd>(
    type: number,
    body: object | null,
    options: {
      itemType: number
      endType: number
      timeoutMs?: number
      isTerminal?: (body: unknown) => boolean
    },
  ): Promise<{ items: TItem[]; end: TEnd }> {
    this.assertReady()
    const requestId = ++this.requestCounter

    return new Promise<{ items: TItem[]; end: TEnd }>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(requestId)
        reject(new Error(`request ${msgTypeName(type)} timed out`))
      }, options.timeoutMs ?? this.options.requestTimeoutMs)

      const stream = {
        itemType: options.itemType,
        endType: options.endType,
        items: [] as unknown[],
        isTerminal: options.isTerminal,
      }
      this.pending.set(requestId, {
        resolve: (envelope) =>
          resolve({ items: stream.items as TItem[], end: envelope.body as TEnd }),
        reject,
        timer,
        stream,
      })

      try {
        this.write(type, body, requestId)
      } catch (error) {
        clearTimeout(timer)
        this.pending.delete(requestId)
        reject(error)
      }
    })
  }

  private assertReady(): void {
    if (this._state !== 'ready') {
      throw new Error(`client not ready (state: ${this._state})`)
    }
  }
}
