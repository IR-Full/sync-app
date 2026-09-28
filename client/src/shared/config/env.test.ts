import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/**
 * `readEnv` and `readIceServers` run at module scope, so every case here needs a
 * fresh module registry — a plain import would freeze whatever the first test
 * happened to set.
 */
async function loadConfig(env: Record<string, string | undefined>) {
  vi.resetModules()
  for (const [key, value] of Object.entries(env)) {
    // `undefined`, not `''`: Next inlines an unset NEXT_PUBLIC_* as undefined,
    // and the module tells the two apart (`?? 'web/0.1'` keeps an empty string).
    vi.stubEnv(key, value === '' ? undefined : value)
  }
  return import('./env')
}

beforeEach(() => vi.resetModules())
afterEach(() => {
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
})

describe('appEnv', () => {
  it('takes an explicit NEXT_PUBLIC_APP_ENV', async () => {
    for (const value of ['development', 'stage', 'production'] as const) {
      const { appEnv } = await loadConfig({ NEXT_PUBLIC_APP_ENV: value })
      expect(appEnv).toBe(value)
    }
  })

  it('ignores a value that is not a known environment', async () => {
    // A typo in a deploy config must not produce a fourth, undefined environment
    // that `isProduction` then answers `false` for.
    const { appEnv } = await loadConfig({ NEXT_PUBLIC_APP_ENV: 'prod' })
    expect(['development', 'production']).toContain(appEnv)
  })

  it('falls back to development when nothing is set', async () => {
    const { appEnv } = await loadConfig({
      NEXT_PUBLIC_APP_ENV: '',
      NODE_ENV: 'development',
    })
    expect(appEnv).toBe('development')
  })

  it('marks only production as production', async () => {
    const stage = await loadConfig({ NEXT_PUBLIC_APP_ENV: 'stage' })
    expect(stage.config.isProduction).toBe(false)

    const production = await loadConfig({ NEXT_PUBLIC_APP_ENV: 'production' })
    expect(production.config.isProduction).toBe(true)
  })
})

describe('gatewayUrl', () => {
  it('uses an explicitly configured url', async () => {
    const { config } = await loadConfig({
      NEXT_PUBLIC_SYNCAPP_WS_URL: 'wss://gateway.example/ws',
    })
    expect(config.gatewayUrl).toBe('wss://gateway.example/ws')
  })

  /**
   * A build served from the same host as the gateway needs no explicit URL — and
   * the scheme has to follow the page, because a browser refuses a `ws://`
   * socket from an `https://` document.
   */
  it('derives wss from an https page', async () => {
    const { config } = await loadConfig({ NEXT_PUBLIC_SYNCAPP_WS_URL: '' })
    vi.stubGlobal('window', {
      location: { protocol: 'https:', host: 'chat.example.com' },
    })
    expect(config.gatewayUrl).toBe('wss://chat.example.com/ws')
  })

  it('derives ws from an http page', async () => {
    const { config } = await loadConfig({ NEXT_PUBLIC_SYNCAPP_WS_URL: '' })
    vi.stubGlobal('window', {
      location: { protocol: 'http:', host: 'localhost:3000' },
    })
    expect(config.gatewayUrl).toBe('ws://localhost:3000/ws')
  })

  it('keeps the port when deriving from the page', async () => {
    const { config } = await loadConfig({ NEXT_PUBLIC_SYNCAPP_WS_URL: '' })
    vi.stubGlobal('window', {
      location: { protocol: 'https:', host: 'chat.example.com:8443' },
    })
    expect(config.gatewayUrl).toContain(':8443')
  })

  it('re-reads the page origin on each access', async () => {
    // It is a getter precisely so a value captured during SSR cannot leak into
    // the client render, where `window` finally exists.
    const { config } = await loadConfig({ NEXT_PUBLIC_SYNCAPP_WS_URL: '' })
    vi.stubGlobal('window', { location: { protocol: 'http:', host: 'a.test' } })
    expect(config.gatewayUrl).toBe('ws://a.test/ws')

    vi.stubGlobal('window', { location: { protocol: 'https:', host: 'b.test' } })
    expect(config.gatewayUrl).toBe('wss://b.test/ws')
  })

  it('prefers the configured url over the page origin', async () => {
    const { config } = await loadConfig({
      NEXT_PUBLIC_SYNCAPP_WS_URL: 'wss://explicit.example/ws',
    })
    vi.stubGlobal('window', { location: { protocol: 'http:', host: 'localhost:3000' } })
    expect(config.gatewayUrl).toBe('wss://explicit.example/ws')
  })
})

describe('iceServers', () => {
  it('defaults to a public STUN server', async () => {
    const { config } = await loadConfig({ NEXT_PUBLIC_ICE_SERVERS: '' })
    expect(config.iceServers).toEqual([{ urls: 'stun:stun.l.google.com:19302' }])
  })

  it('parses a configured server list', async () => {
    const { config } = await loadConfig({
      NEXT_PUBLIC_ICE_SERVERS: JSON.stringify([
        { urls: 'stun:stun.example:3478' },
        { urls: 'turn:turn.example:3478', username: 'u', credential: 'p' },
      ]),
    })
    expect(config.iceServers).toHaveLength(2)
    expect(config.iceServers[1]).toMatchObject({ username: 'u' })
  })

  it('accepts an empty list, which means host candidates only', async () => {
    // A documented LAN-only configuration — it must survive the "falsy" check
    // that the default branch uses.
    const { config } = await loadConfig({ NEXT_PUBLIC_ICE_SERVERS: '[]' })
    expect(config.iceServers).toEqual([])
  })

  it('falls back to no servers rather than crashing on invalid JSON', async () => {
    // A malformed env var would otherwise throw during module evaluation and
    // take the whole app down at import time, not just calls.
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { config } = await loadConfig({ NEXT_PUBLIC_ICE_SERVERS: '{not json' })

    expect(config.iceServers).toEqual([])
    expect(warn).toHaveBeenCalled()
  })

  it('falls back to no servers when the JSON is not an array', async () => {
    const { config } = await loadConfig({
      NEXT_PUBLIC_ICE_SERVERS: JSON.stringify({ urls: 'stun:x' }),
    })
    expect(config.iceServers).toEqual([])
  })
})

describe('protocol tuning', () => {
  it('requests a history page the gateway will not truncate', async () => {
    // The gateway caps HISTORY at 100; asking for more silently gets fewer,
    // which would make the "done" heuristic wrong.
    const { config } = await loadConfig({})
    expect(config.historyPageSize).toBeGreaterThan(0)
    expect(config.historyPageSize).toBeLessThanOrEqual(100)
  })

  it('throttles typing frames slower than the indicator expires', async () => {
    // Otherwise the indicator would blink off between two of our own frames.
    const { config } = await loadConfig({})
    expect(config.typingThrottleMs).toBeLessThan(config.typingTimeoutMs)
  })

  it('throttles typing no faster than the gateway`s own limit', async () => {
    // The gateway allows roughly one TYPING per chat every two seconds and
    // discards the rest; sending faster just burns rate-limit budget.
    const { config } = await loadConfig({})
    expect(config.typingThrottleMs).toBeGreaterThanOrEqual(2000)
  })

  it('uses a client version that names the platform', async () => {
    const { config } = await loadConfig({ NEXT_PUBLIC_CLIENT_VERSION: '' })
    expect(config.clientVersion).toMatch(/^web\//)
  })

  it('takes an explicit client version', async () => {
    const { config } = await loadConfig({ NEXT_PUBLIC_CLIENT_VERSION: 'web/2.0' })
    expect(config.clientVersion).toBe('web/2.0')
  })
})
