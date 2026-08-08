import type { Wire } from '@/shared/api'

/**
 * `pending` — written to the outbox, not yet acknowledged (or waiting for a
 *             connection). `sent` — the gateway returned a SEND_ACK, so the row
 *             is durable and has a chat sequence. `failed` — the send was
 *             rejected for a reason retrying will not fix.
 */
export type MessageStatus = 'pending' | 'sent' | 'failed'

/** Typed media riding along with a message (voice note, video note, file, image). */
export interface MessageAttachment {
  kind: string
  mediaRef: string
  filename: string
  mime: string
  size: number
  durationMs: number
  waveform: number[]
  width: number
  height: number
  thumbRef: string
}

/** Provenance kept when a message is forwarded, so the origin stays visible. */
export interface ForwardOrigin {
  chatId: string
  messageId: string
  senderId: string
}

export interface ChatMessage {
  /** server message id once acknowledged; the dedup key while pending */
  id: string
  chatId: string
  senderId: string
  text: string
  /** server-assigned per-chat ordering; 0 while pending */
  seq: number
  timestamp: number
  edited: boolean
  deleted: boolean
  replyTo: string
  mediaRef: string
  attachment: MessageAttachment | null
  forward: ForwardOrigin | null
  /** root of the reply branch this message belongs to ('' when top level) */
  threadRoot: string
  replyCount: number
  /** unix ms after which a self-destructing message is gone (0 = never) */
  expiresAt: number
  outgoing: boolean
  status: MessageStatus
  /** idempotency key — kept so a retry resolves to the same server row */
  dedupKey?: string
  /** why a failed send failed, for the retry affordance */
  error?: string
}

export function fromWire(message: Wire.NewMessage, selfId: string): ChatMessage {
  return {
    id: message.messageId,
    chatId: message.chatId,
    senderId: message.senderId,
    text: message.text,
    seq: message.chatSeq,
    timestamp: message.timestamp,
    edited: message.edited,
    deleted: message.deleted,
    replyTo: message.replyTo,
    mediaRef: message.mediaRef,
    attachment: message.attachment
      ? {
          kind: message.attachment.kind,
          mediaRef: message.attachment.mediaRef,
          filename: message.attachment.filename,
          mime: message.attachment.mime,
          size: message.attachment.size,
          durationMs: message.attachment.durationMs,
          waveform: message.attachment.waveform ?? [],
          width: message.attachment.width,
          height: message.attachment.height,
          thumbRef: message.attachment.thumbRef,
        }
      : null,
    forward: message.forward
      ? {
          chatId: message.forward.chatId,
          messageId: message.forward.messageId,
          senderId: message.forward.senderId,
        }
      : null,
    threadRoot: message.threadRoot,
    replyCount: message.replyCount,
    expiresAt: message.expiresAt,
    outgoing: message.senderId === selfId,
    status: 'sent',
  }
}

/** A blank outgoing message, so call sites only spell out what differs. */
export function draftMessage(overrides: Partial<ChatMessage> & { id: string }): ChatMessage {
  return {
    chatId: '',
    senderId: '',
    text: '',
    seq: 0,
    timestamp: Date.now(),
    edited: false,
    deleted: false,
    replyTo: '',
    mediaRef: '',
    attachment: null,
    forward: null,
    threadRoot: '',
    replyCount: 0,
    expiresAt: 0,
    outgoing: true,
    status: 'pending',
    ...overrides,
  }
}

/** Newest last — the order a chat transcript is read in. */
export function bySequence(a: ChatMessage, b: ChatMessage): number {
  if (a.seq !== b.seq) return a.seq - b.seq
  return a.timestamp - b.timestamp
}

/** True once a self-destructing message's deadline has passed. */
export function hasExpired(message: ChatMessage, now = Date.now()): boolean {
  return message.expiresAt > 0 && message.expiresAt <= now
}
