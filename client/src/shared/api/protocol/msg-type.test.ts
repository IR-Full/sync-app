import { describe, expect, it } from 'vitest'

import { MsgType, msgTypeName } from './msg-type'

const entries = Object.entries(MsgType) as [string, number][]

describe('MsgType', () => {
  /**
   * These numbers are the wire contract with `server/pkg/wire/constants.go`.
   * Nothing at runtime validates them: a duplicated or renumbered constant makes
   * the client encode a body the server parses as a different message entirely,
   * and the first sign of it is a confusing server-side error.
   */
  it('allocates each number exactly once', () => {
    const byNumber = new Map<number, string[]>()
    for (const [name, value] of entries) {
      byNumber.set(value, [...(byNumber.get(value) ?? []), name])
    }
    const duplicates = [...byNumber].filter(([, names]) => names.length > 1)
    expect(duplicates).toEqual([])
  })

  it('keeps every type inside the single-byte range the frame header allows', () => {
    for (const [name, value] of entries) {
      expect({ name, inRange: value >= 0 && value <= 0xff }).toEqual({ name, inRange: true })
    }
  })

  it('uses integers only', () => {
    for (const [name, value] of entries) {
      expect({ name, integer: Number.isInteger(value) }).toEqual({ name, integer: true })
    }
  })

  it('reserves 0', () => {
    // A zero type is what an uninitialised or truncated frame looks like, so it
    // must never name a real message.
    expect(MsgType.RESERVED).toBe(0)
  })

  it('pins the handshake and auth block', () => {
    expect(MsgType.HELLO).toBe(1)
    expect(MsgType.WELCOME).toBe(2)
    expect(MsgType.AUTH).toBe(3)
    expect(MsgType.AUTH_OK).toBe(4)
    expect(MsgType.AUTH_ERR).toBe(5)
  })

  it('pins the liveness and transport-control types', () => {
    expect(MsgType.PING).toBe(6)
    expect(MsgType.PONG).toBe(7)
    expect(MsgType.T_ACK).toBe(30)
    expect(MsgType.RESUME).toBe(31)
    expect(MsgType.RESUME_OK).toBe(32)
    expect(MsgType.ERROR).toBe(40)
  })

  it('pins the messaging block', () => {
    expect(MsgType.SEND).toBe(8)
    expect(MsgType.SEND_ACK).toBe(9)
    expect(MsgType.NEW).toBe(10)
    expect(MsgType.READ).toBe(11)
    expect(MsgType.READ_UPD).toBe(12)
    expect(MsgType.TYPING).toBe(13)
    expect(MsgType.PRESENCE).toBe(14)
    expect(MsgType.EDIT).toBe(15)
    expect(MsgType.DELETE).toBe(16)
    expect(MsgType.HISTORY).toBe(17)
    expect(MsgType.HISTORY_OK).toBe(18)
  })

  it('pins the secret-chat block', () => {
    expect(MsgType.KEY_PUBLISH).toBe(50)
    expect(MsgType.KEY_FETCH).toBe(51)
    expect(MsgType.KEY_BUNDLE).toBe(52)
    expect(MsgType.SECRET_SEND).toBe(53)
    expect(MsgType.SECRET_RECV).toBe(54)
    expect(MsgType.KEY_FETCH_ALL).toBe(55)
    expect(MsgType.KEY_BUNDLES).toBe(56)
  })

  it('pins the newest types, which are the ones most likely to drift', () => {
    expect(MsgType.CHAT_LIST).toBe(123)
    expect(MsgType.CHATS).toBe(124)
    expect(MsgType.PROFILE_GET).toBe(125)
    expect(MsgType.PROFILE_SET).toBe(126)
    expect(MsgType.PROFILE).toBe(127)
    expect(MsgType.DELIVERED).toBe(128)
  })
})

describe('msgTypeName', () => {
  it('names every declared type', () => {
    for (const [name, value] of entries) {
      expect(msgTypeName(value)).toBe(name)
    }
  })

  it('renders an unknown type with its number instead of throwing', () => {
    // The protocol requires unknown types to be ignorable, and the number is the
    // only thing that makes a log line about one actionable.
    expect(msgTypeName(200)).toBe('UNKNOWN(200)')
  })

  it('handles a type in a reserved gap', () => {
    // 19 sits between HISTORY_OK and MEDIA_INIT — reserved for future messaging
    // types, so a build talking to a newer server can meet it.
    expect(msgTypeName(19)).toBe('UNKNOWN(19)')
  })

  it('handles values outside the byte range without throwing', () => {
    expect(msgTypeName(-1)).toBe('UNKNOWN(-1)')
    expect(msgTypeName(999)).toBe('UNKNOWN(999)')
  })
})
