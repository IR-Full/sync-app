/**
 * Byte/string conversions for the E2E layer.
 *
 * Everything crossing the wire uses **standard base64 with padding**, matching
 * Go's `base64.StdEncoding` — the encoding the server's own integration test
 * uses for prekey bundles and ciphertext. URL-safe base64 would silently produce
 * keys the Go side cannot parse.
 */

export function toBase64(bytes: Uint8Array): string {
  let binary = ''
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i])
  return btoa(binary)
}

export function fromBase64(value: string): Uint8Array {
  if (!value) return new Uint8Array(0)
  const binary = atob(value)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

const encoder = new TextEncoder()
const decoder = new TextDecoder()

export function toUtf8(value: string): Uint8Array {
  return encoder.encode(value)
}

export function fromUtf8(bytes: Uint8Array): string {
  return decoder.decode(bytes)
}

export function concatBytes(...chunks: Uint8Array[]): Uint8Array {
  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0)
  const out = new Uint8Array(total)
  let offset = 0
  for (const chunk of chunks) {
    out.set(chunk, offset)
    offset += chunk.length
  }
  return out
}
