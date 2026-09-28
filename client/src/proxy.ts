import { NextResponse, type NextRequest } from 'next/server'

/**
 * Per-request security headers.
 *
 * The Content-Security-Policy used to live in `next.config.ts`, which meant it
 * had to be a constant — and a constant CSP cannot carry a nonce, so
 * `script-src` kept `'unsafe-inline'`. That single token is most of the value of
 * having a CSP at all: with it, an injected `<script>` runs, and this app keeps
 * long-term E2E identity keys and the session token in `localStorage`, so one
 * XSS is not a defacement, it is a permanent key compromise.
 *
 * A header built per request can mint a fresh nonce, which is why this file
 * exists. Next.js finds the `'nonce-…'` in the CSP it receives and stamps it
 * onto every script it emits itself (framework runtime, route bundles); the one
 * inline script this app writes by hand — the pre-paint theme script in
 * `app/layout.tsx` — reads it back from `x-nonce`.
 *
 * **What this costs.** Nonces require dynamic rendering: a page baked at build
 * time has no request, so there is no nonce to bake in. Static optimization and
 * CDN caching of HTML are therefore off. For this app that is close to free —
 * every route is a client-rendered shell that immediately opens a WebSocket, so
 * there was never a useful cacheable HTML response. For a content site the trade
 * would look very different, which is why it is written down rather than
 * assumed.
 *
 * Deliberately NOT covered: `style-src` keeps `'unsafe-inline'`. A nonce can
 * authorise a `<style>` element but not a `style=""` attribute, and the poll
 * result bar sets its width that way because the width is data. Dropping the
 * token would mean inlining a stylesheet per possible percentage. Style
 * injection is also a far weaker primitive than script injection — it can
 * restyle the page, not read a key.
 */

/**
 * Origin of the gateway socket, so `connect-src` can name it.
 *
 * Read from the environment on every request rather than captured once: this
 * runs in the proxy, which Next.js may deploy separately from the server
 * bundle, and a value frozen at module load would be the build's idea of the
 * gateway rather than the deployment's. An empty or malformed value yields
 * nothing, and `'self'` already covers the same-origin case.
 */
export function socketOrigins(): string[] {
  const url = process.env.NEXT_PUBLIC_SYNCAPP_WS_URL
  if (!url) return []
  try {
    const { origin } = new URL(url)
    // Both schemes: the page fetches media over http(s) from the gateway and
    // opens the protocol socket over ws(s) against the same host.
    return [origin, origin.replace(/^http/, 'ws')]
  } catch {
    return []
  }
}

export function contentSecurityPolicy(nonce: string, isDev: boolean): string {
  const connect = ["'self'", ...socketOrigins()]
  return [
    "default-src 'self'",
    // 'strict-dynamic' lets the nonced Next.js bootstrap load the chunks it
    // needs without naming each one, and — the point — makes host-source
    // allowances irrelevant, so an injected script cannot ride in on 'self'.
    // 'unsafe-eval' only in development: React uses eval to rebuild server
    // stacks for the error overlay.
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${isDev ? " 'unsafe-eval'" : ''}`,
    // See the note above: style attributes cannot carry a nonce.
    "style-src 'self' 'unsafe-inline'",
    // blob: for object URLs of media decoded locally; data: for inline SVGs.
    "img-src 'self' blob: data:",
    "media-src 'self' blob:",
    "font-src 'self' data:",
    `connect-src ${connect.join(' ')}`,
    "worker-src 'self'",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "object-src 'none'",
  ].join('; ')
}

export function proxy(request: NextRequest) {
  const isDev = process.env.NODE_ENV === 'development'
  // crypto.randomUUID is the platform's CSPRNG. The nonce only has to be
  // unguessable and unique per response; 122 bits of randomness is both.
  const nonce = Buffer.from(crypto.randomUUID()).toString('base64')
  const csp = contentSecurityPolicy(nonce, isDev)

  const requestHeaders = new Headers(request.headers)
  requestHeaders.set('x-nonce', nonce)
  // Next.js parses the nonce out of the CSP on the REQUEST to stamp its own
  // script tags, so this has to be set on both sides, not just the response.
  requestHeaders.set('Content-Security-Policy', csp)

  const response = NextResponse.next({ request: { headers: requestHeaders } })
  response.headers.set('Content-Security-Policy', csp)

  // HSTS. Absent until now, which left the first request of every session
  // downgradeable: a network attacker answers plain http:// once and keeps the
  // user there. Two years with subdomains is the preload-eligible value.
  //
  // Not sent in development, where the app runs on http://localhost and a
  // browser that has pinned localhost to HTTPS is a browser that cannot load it
  // again until the pin expires — a footgun that outlives the mistake.
  if (!isDev) {
    response.headers.set(
      'Strict-Transport-Security',
      'max-age=63072000; includeSubDomains; preload',
    )
  }

  return response
}

export const config = {
  matcher: [
    {
      // Static assets and the image optimizer need no policy — they are not
      // documents and cannot execute anything. Prefetches are excluded because
      // the response they produce is never the one that renders, so spending a
      // nonce on them only churns the header.
      source: '/((?!_next/static|_next/image|favicon.ico|push-sw.js).*)',
      missing: [
        { type: 'header', key: 'next-router-prefetch' },
        { type: 'header', key: 'purpose', value: 'prefetch' },
      ],
    },
  ],
}
