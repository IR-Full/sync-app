/**
 * A WebSocket stand-in for tests, driven by hand.
 *
 * jsdom ships a real WebSocket that tries to open a real TCP connection, so a
 * test using it would be slow, flaky and dependent on a listening gateway. This
 * class implements just the surface `SyncAppClient` touches and hands the test
 * the other end of the wire: `open()`, `deliver()` and `serverClose()` play the
 * gateway, while `sent` records what the client wrote.
 *
 * Not exported from the package barrel — it exists only for `*.test.ts`.
 */
import { decodeBody, encodeBody, hasBody } from './codec'
import { decodeEnvelope, encodeEnvelope, type Envelope } from './envelope'
import { decodeFrame, encodeFrame } from './frame'

type Handler<E> = ((event: E) => void) | null

export class FakeSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3

  /** Every socket the client has dialled, oldest first — reconnects append. */
  static instances: FakeSocket[] = []

  static reset(): void {
    FakeSocket.instances = []
  }

  static get last(): FakeSocket {
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('no socket has been dialled')
    return socket
  }

  readyState: number = FakeSocket.CONNECTING
  binaryType = 'blob'
  /** Raw frames the client has written, in order. */
  readonly sent: Uint8Array[] = []

  onopen: Handler<Event> = null
  onmessage: Handler<{ data: ArrayBuffer }> = null
  onerror: Handler<Event> = null
  onclose: Handler<CloseEvent> = null

  constructor(readonly url: string) {
    FakeSocket.instances.push(this)
  }

  send(data: Uint8Array): void {
    if (this.readyState !== FakeSocket.OPEN) throw new Error('send on a non-open socket')
    this.sent.push(new Uint8Array(data))
  }

  /**
   * Mirrors the browser: `close()` returns immediately and the close event lands
   * later. Firing it synchronously would let `handleDisconnect` run twice for one
   * drop — the client's own guard (`this.socket === socket`) depends on the real
   * ordering, so the fake has to reproduce it.
   */
  close(): void {
    if (this.readyState === FakeSocket.CLOSED) return
    this.readyState = FakeSocket.CLOSED
    queueMicrotask(() => this.onclose?.({} as CloseEvent))
  }

  // ------------------------------------------------------- test-side driving

  /** The TCP/WS handshake completed. */
  open(): void {
    this.readyState = FakeSocket.OPEN
    this.onopen?.({} as Event)
  }

  /** The gateway dropped us (as opposed to the client closing). */
  serverClose(): void {
    this.readyState = FakeSocket.CLOSED
    this.onclose?.({} as CloseEvent)
  }

  /** Pushes one already-framed message at the client. */
  deliverFrame(frame: Uint8Array): void {
    const buffer = frame.buffer.slice(frame.byteOffset, frame.byteOffset + frame.byteLength)
    this.onmessage?.({ data: buffer as ArrayBuffer })
  }

  /** Frames and pushes one envelope. `seq` defaults to a running counter. */
  deliver(
    type: number,
    body?: object | null,
    options: { requestId?: number; seq?: number } = {},
  ): void {
    this.serverSeq = options.seq ?? this.serverSeq + 1
    this.deliverFrame(
      encodeFrame(
        encodeEnvelope({
          type,
          seq: this.serverSeq,
          ack: 0,
          requestId: options.requestId ?? 0,
          body: encodeBodyOrEmpty(type, body),
        }),
      ),
    )
  }

  private serverSeq = 0

  // ------------------------------------------------------ test-side reading

  /** Decoded envelope headers for everything the client wrote. */
  get envelopes(): Envelope[] {
    return this.sent.map((frame) => decodeEnvelope(decodeFrame(frame)))
  }

  /** Envelope headers plus decoded bodies. */
  get frames(): { type: number; seq: number; ack: number; requestId: number; body: unknown }[] {
    return this.envelopes.map((envelope) => ({
      type: envelope.type,
      seq: envelope.seq,
      ack: envelope.ack,
      requestId: envelope.requestId,
      body:
        hasBody(envelope.type) && envelope.body.length >= 0
          ? decodeBody(envelope.type, envelope.body)
          : null,
    }))
  }

  /** The most recent frame the client wrote of this type, or undefined. */
  lastOf(type: number) {
    return this.frames.filter((frame) => frame.type === type).at(-1)
  }
}

function encodeBodyOrEmpty(type: number, body?: object | null): Uint8Array {
  // Re-uses the client's own encoder: hand-rolled bytes would drift from the
  // schema the moment body.proto changes, and the point of this fake is the
  // connection lifecycle, not a second protobuf implementation.
  if (body == null || !hasBody(type)) return new Uint8Array(0)
  return encodeBody(type, body)
}
