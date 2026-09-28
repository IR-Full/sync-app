import { afterEach, describe, expect, it, vi } from 'vitest'

import { contentSecurityPolicy, socketOrigins } from './proxy'

afterEach(() => {
  vi.unstubAllEnvs()
})

describe('contentSecurityPolicy', () => {
  /**
   * The whole reason this moved out of next.config.ts: a constant header cannot
   * carry a nonce, so script-src kept 'unsafe-inline' — and with that token an
   * injected script runs, which in this app means the E2E identity keys in
   * localStorage are gone.
   */
  it('authorises scripts by nonce, never by unsafe-inline', () => {
    const csp = contentSecurityPolicy('n0nc3', false)
    const scriptSrc = csp.split('; ').find((d) => d.startsWith('script-src'))

    expect(scriptSrc).toContain("'nonce-n0nc3'")
    expect(scriptSrc).not.toContain("'unsafe-inline'")
  })

  it("uses 'strict-dynamic' so an injected script cannot ride in on 'self'", () => {
    expect(contentSecurityPolicy('n', false)).toContain("'strict-dynamic'")
  })

  it("allows 'unsafe-eval' only in development", () => {
    expect(contentSecurityPolicy('n', true)).toContain("'unsafe-eval'")
    expect(contentSecurityPolicy('n', false)).not.toContain("'unsafe-eval'")
  })

  /**
   * A nonce authorises a <style> element but not a style="" attribute, and the
   * poll result bar sets its width that way because the width is data. This is
   * asserted rather than left implicit so that removing the token is a decision
   * with a failing test attached, not a tidy-up.
   */
  it("keeps 'unsafe-inline' for styles, which nonces cannot cover", () => {
    const styleSrc = contentSecurityPolicy('n', false)
      .split('; ')
      .find((d) => d.startsWith('style-src'))
    expect(styleSrc).toContain("'unsafe-inline'")
  })

  it('names the gateway socket in connect-src, in both schemes', () => {
    vi.stubEnv('NEXT_PUBLIC_SYNCAPP_WS_URL', 'https://gw.example:8443/ws')
    const connect = contentSecurityPolicy('n', false)
      .split('; ')
      .find((d) => d.startsWith('connect-src'))

    expect(connect).toContain("'self'")
    expect(connect).toContain('https://gw.example:8443')
    expect(connect).toContain('wss://gw.example:8443')
  })

  it('keeps the policy usable when the socket URL is absent or malformed', () => {
    vi.stubEnv('NEXT_PUBLIC_SYNCAPP_WS_URL', '')
    expect(contentSecurityPolicy('n', false)).toContain("connect-src 'self'")

    vi.stubEnv('NEXT_PUBLIC_SYNCAPP_WS_URL', 'not a url')
    expect(contentSecurityPolicy('n', false)).toContain("connect-src 'self'")
  })

  it('keeps the directives that do not depend on a request', () => {
    const csp = contentSecurityPolicy('n', false)
    for (const directive of [
      "default-src 'self'",
      "frame-ancestors 'none'",
      "object-src 'none'",
      "base-uri 'self'",
      "form-action 'self'",
    ]) {
      expect(csp).toContain(directive)
    }
  })
})

describe('socketOrigins', () => {
  it('returns nothing for an unset or unparseable URL, so connect-src stays valid', () => {
    vi.stubEnv('NEXT_PUBLIC_SYNCAPP_WS_URL', '')
    expect(socketOrigins()).toEqual([])

    vi.stubEnv('NEXT_PUBLIC_SYNCAPP_WS_URL', '://broken')
    expect(socketOrigins()).toEqual([])
  })

  it('derives both the http and ws form of one origin', () => {
    vi.stubEnv('NEXT_PUBLIC_SYNCAPP_WS_URL', 'ws://localhost:8080/ws')
    expect(socketOrigins()).toEqual(['ws://localhost:8080', 'ws://localhost:8080'])
  })
})
