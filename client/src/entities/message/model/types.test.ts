import { describe, expect, it, vi } from 'vitest'

import type { Wire } from '@/shared/api'
import { bySequence, draftMessage, fromWire, hasExpired, type ChatMessage } from './types'

/** A NEW frame as the codec decodes it — proto3 defaults materialised. */
function wire(overrides: Partial<Wire.NewMessage> = {}): Wire.NewMessage {
  return {
    messageId: 'm1',
    chatId: 'c1',
    senderId: 'u2',
    chatSeq: 7,
    text: 'hello',
    mediaRef: '',
    replyTo: '',
    edited: false,
    deleted: false,
    timestamp: 1_000,
    threadRoot: '',
    replyCount: 0,
    expiresAt: 0,
    ...overrides,
  } as Wire.NewMessage
}

describe('fromWire', () => {
  it('maps the scalar fields across', () => {
    expect(fromWire(wire(), 'self')).toMatchObject({
      id: 'm1',
      chatId: 'c1',
      senderId: 'u2',
      text: 'hello',
      seq: 7,
      timestamp: 1_000,
    })
  })

  it('marks an incoming message as received', () => {
    expect(fromWire(wire({ senderId: 'u2' }), 'self').outgoing).toBe(false)
  })

  /**
   * Fanout echoes a message back to its own sender, so this path runs for our
   * own messages too. Getting it wrong puts our own bubbles on the wrong side of
   * the transcript.
   */
  it('marks our own echoed message as outgoing', () => {
    expect(fromWire(wire({ senderId: 'self' }), 'self').outgoing).toBe(true)
  })

  it('marks a wire message as sent, never pending', () => {
    // It came from the server, so it is durable by definition; 'pending' would
    // render a spinner on a message that has already landed.
    expect(fromWire(wire(), 'self').status).toBe('sent')
  })

  it('carries the tombstone flags', () => {
    const message = fromWire(wire({ edited: true, deleted: true }), 'self')
    expect(message).toMatchObject({ edited: true, deleted: true })
  })

  it('carries the reply and thread anchors', () => {
    const message = fromWire(
      wire({ replyTo: 'm0', threadRoot: 'root-1', replyCount: 3 }),
      'self',
    )
    expect(message).toMatchObject({ replyTo: 'm0', threadRoot: 'root-1', replyCount: 3 })
  })

  it('carries the self-destruct deadline', () => {
    // A dropped deadline means the message never disappears.
    expect(fromWire(wire({ expiresAt: 1_700_000 }), 'self').expiresAt).toBe(1_700_000)
  })

  it('maps every attachment field', () => {
    const attachment = {
      kind: 'voice',
      mediaRef: 'media-1',
      filename: 'note.ogg',
      mime: 'audio/ogg',
      size: 2048,
      durationMs: 3400,
      waveform: [10, 40, 90],
      width: 0,
      height: 0,
      thumbRef: 'thumb-1',
    }

    expect(fromWire(wire({ attachment } as never), 'self').attachment).toEqual(attachment)
  })

  it('defaults an absent waveform to an empty array', () => {
    // The bubble maps over it to draw the bars; `undefined.map` would take down
    // the whole transcript rather than render one voice note badly.
    const message = fromWire(
      wire({ attachment: { kind: 'voice', mediaRef: 'm', waveform: undefined } } as never),
      'self',
    )
    expect(message.attachment?.waveform).toEqual([])
  })

  it('leaves a message with no attachment at null', () => {
    // An empty object here would make the bubble render an attachment card for
    // a plain text message.
    expect(fromWire(wire(), 'self').attachment).toBeNull()
  })

  it('maps forward provenance', () => {
    const forward = { chatId: 'c0', messageId: 'm0', senderId: 'u9' }
    expect(fromWire(wire({ forward } as never), 'self').forward).toEqual(forward)
  })

  it('leaves a message that was not forwarded at null', () => {
    expect(fromWire(wire(), 'self').forward).toBeNull()
  })

  it('does not invent a dedup key for an inbound message', () => {
    // Only our own optimistic rows carry one; a fabricated key here could
    // collide with a real outbox item and collapse two different messages.
    expect(fromWire(wire(), 'self').dedupKey).toBeUndefined()
  })
})

describe('draftMessage', () => {
  it('requires only an id', () => {
    expect(() => draftMessage({ id: 'draft-1' })).not.toThrow()
  })

  it('defaults to a pending outgoing message', () => {
    // Every call site is the composer, so these are the right defaults — and a
    // draft that defaulted to 'sent' would show a delivered tick immediately.
    const message = draftMessage({ id: 'draft-1' })
    expect(message.status).toBe('pending')
    expect(message.outgoing).toBe(true)
  })

  it('has no sequence until the server assigns one', () => {
    // 0 is what `flattenHistory` keys on to file a pending message at the end of
    // the transcript rather than at the top.
    expect(draftMessage({ id: 'draft-1' }).seq).toBe(0)
  })

  it('lets overrides win over every default', () => {
    const message = draftMessage({
      id: 'server-1',
      seq: 9,
      status: 'sent',
      outgoing: false,
      text: 'hello',
    })
    expect(message).toMatchObject({ seq: 9, status: 'sent', outgoing: false, text: 'hello' })
  })

  it('stamps the current time by default', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-06T12:00:00Z'))
    try {
      expect(draftMessage({ id: 'draft-1' }).timestamp).toBe(Date.now())
    } finally {
      vi.useRealTimers()
    }
  })

  it('fills in every field the type declares', () => {
    // A missing field would be `undefined` on a typed object, which type-checks
    // through `Partial` spreads and then renders as blank.
    const message = draftMessage({ id: 'draft-1' })
    for (const key of [
      'chatId',
      'senderId',
      'text',
      'seq',
      'timestamp',
      'edited',
      'deleted',
      'replyTo',
      'mediaRef',
      'attachment',
      'forward',
      'threadRoot',
      'replyCount',
      'expiresAt',
      'outgoing',
      'status',
    ] as const) {
      expect({ key, defined: message[key] !== undefined }).toEqual({ key, defined: true })
    }
  })
})

describe('bySequence', () => {
  const at = (seq: number, timestamp = 0): ChatMessage =>
    draftMessage({ id: `m${seq}`, seq, timestamp })

  it('orders by sequence', () => {
    expect(bySequence(at(1), at(2))).toBeLessThan(0)
    expect(bySequence(at(2), at(1))).toBeGreaterThan(0)
  })

  it('breaks a sequence tie by timestamp', () => {
    // Two pending messages both sit at seq 0; without the tie-break they would
    // sort unstably and the composer's own messages would reorder themselves.
    expect(bySequence(at(0, 1_000), at(0, 2_000))).toBeLessThan(0)
  })

  it('reports equal for the same message', () => {
    expect(bySequence(at(5, 1_000), at(5, 1_000))).toBe(0)
  })

  it('sorts a transcript oldest first', () => {
    const sorted = [at(3, 30), at(1, 10), at(2, 20)].sort(bySequence)
    expect(sorted.map((message) => message.seq)).toEqual([1, 2, 3])
  })
})

describe('hasExpired', () => {
  it('is false for a message with no deadline', () => {
    // TTL 0 means "keep forever"; treating it as an epoch deadline would hide
    // every ordinary message.
    expect(hasExpired(draftMessage({ id: 'm1', expiresAt: 0 }), 1_000)).toBe(false)
  })

  it('is false before the deadline', () => {
    expect(hasExpired(draftMessage({ id: 'm1', expiresAt: 2_000 }), 1_000)).toBe(false)
  })

  it('is true at the deadline', () => {
    expect(hasExpired(draftMessage({ id: 'm1', expiresAt: 1_000 }), 1_000)).toBe(true)
  })

  it('is true past the deadline', () => {
    expect(hasExpired(draftMessage({ id: 'm1', expiresAt: 1_000 }), 2_000)).toBe(true)
  })

  it('uses the current time by default', () => {
    expect(hasExpired(draftMessage({ id: 'm1', expiresAt: Date.now() - 1 }))).toBe(true)
    expect(hasExpired(draftMessage({ id: 'm1', expiresAt: Date.now() + 60_000 }))).toBe(false)
  })
})
