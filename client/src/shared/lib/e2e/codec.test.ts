import { describe, expect, it } from 'vitest'

import { concatBytes, fromBase64, fromUtf8, toBase64, toUtf8 } from './codec'

describe('toBase64 / fromBase64', () => {
  /**
   * The wire contract is Go's `base64.StdEncoding`: padded, `+` and `/` as the
   * last two alphabet characters. A URL-safe variant round-trips perfectly
   * against itself, so only a cross-check against known-good vectors catches the
   * mismatch — and the symptom on the Go side is a parse error on a prekey
   * bundle, far from the encoder that caused it.
   */
  it('matches the RFC 4648 test vectors', () => {
    expect(toBase64(toUtf8(''))).toBe('')
    expect(toBase64(toUtf8('f'))).toBe('Zg==')
    expect(toBase64(toUtf8('fo'))).toBe('Zm8=')
    expect(toBase64(toUtf8('foo'))).toBe('Zm9v')
    expect(toBase64(toUtf8('foob'))).toBe('Zm9vYg==')
    expect(toBase64(toUtf8('fooba'))).toBe('Zm9vYmE=')
    expect(toBase64(toUtf8('foobar'))).toBe('Zm9vYmFy')
  })

  it('emits padding, as Go StdEncoding does', () => {
    expect(toBase64(new Uint8Array([0]))).toBe('AA==')
    expect(toBase64(new Uint8Array([0, 0]))).toBe('AAA=')
  })

  it('uses the standard alphabet, not the URL-safe one', () => {
    // 0xFB 0xFF encodes to "+/" in the standard alphabet and "-_" in URL-safe.
    expect(toBase64(new Uint8Array([0xfb, 0xff, 0xbf]))).toBe('+/+/')
  })

  it('round-trips every byte value', () => {
    const all = new Uint8Array(256)
    for (let i = 0; i < 256; i++) all[i] = i
    expect(Array.from(fromBase64(toBase64(all)))).toEqual(Array.from(all))
  })

  it('round-trips a 32-byte key', () => {
    const key = crypto.getRandomValues(new Uint8Array(32))
    expect(Array.from(fromBase64(toBase64(key)))).toEqual(Array.from(key))
  })

  it('decodes the empty string to an empty array', () => {
    // Optional bundle fields (a missing one-time prekey) arrive as "" and are
    // length-checked by the caller, so this must not throw.
    expect(fromBase64('').length).toBe(0)
  })

  it('returns a Uint8Array, not a plain array', () => {
    expect(fromBase64('Zm9v')).toBeInstanceOf(Uint8Array)
  })
})

describe('toUtf8 / fromUtf8', () => {
  it('round-trips ASCII', () => {
    expect(fromUtf8(toUtf8('hello'))).toBe('hello')
  })

  it('round-trips multi-byte text', () => {
    // Message plaintext is arbitrary user input, so non-Latin scripts and emoji
    // are the common case, not an edge case.
    const text = 'Привет, мир! 你好 🔐'
    expect(fromUtf8(toUtf8(text))).toBe(text)
  })

  it('encodes as UTF-8 byte counts, not UTF-16 code units', () => {
    expect(toUtf8('é').length).toBe(2)
    expect(toUtf8('🔐').length).toBe(4)
  })

  it('round-trips the empty string', () => {
    expect(fromUtf8(toUtf8(''))).toBe('')
  })

  it('survives a base64 hop, which is how ciphertext travels', () => {
    const text = 'секретное сообщение 🤫'
    expect(fromUtf8(fromBase64(toBase64(toUtf8(text))))).toBe(text)
  })
})

describe('concatBytes', () => {
  it('joins chunks in order', () => {
    const out = concatBytes(new Uint8Array([1, 2]), new Uint8Array([3]), new Uint8Array([4, 5]))
    expect(Array.from(out)).toEqual([1, 2, 3, 4, 5])
  })

  it('returns an empty array for no chunks', () => {
    expect(concatBytes().length).toBe(0)
  })

  it('skips empty chunks without shifting the offset', () => {
    // X3DH appends a fourth DH output only when a one-time prekey was used; the
    // absent case has to leave the first three outputs byte-identical.
    const out = concatBytes(new Uint8Array([1]), new Uint8Array(0), new Uint8Array([2]))
    expect(Array.from(out)).toEqual([1, 2])
  })

  it('copies rather than aliasing its inputs', () => {
    // The ratchet reuses key buffers; a view onto a caller's array would mutate
    // a secret in place when that buffer is later overwritten.
    const source = new Uint8Array([1, 2, 3])
    const out = concatBytes(source)
    source[0] = 99
    expect(out[0]).toBe(1)
  })

  it('handles the 32+32+32 DH concatenation X3DH performs', () => {
    const dh = () => crypto.getRandomValues(new Uint8Array(32))
    const [a, b, c] = [dh(), dh(), dh()]
    const out = concatBytes(a, b, c)
    expect(out.length).toBe(96)
    expect(Array.from(out.slice(0, 32))).toEqual(Array.from(a))
    expect(Array.from(out.slice(32, 64))).toEqual(Array.from(b))
    expect(Array.from(out.slice(64))).toEqual(Array.from(c))
  })
})
