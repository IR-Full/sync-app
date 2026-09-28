import type { NextConfig } from 'next'

/**
 * Origin of the gateway's HTTP side (media upload/download lives there, not on
 * the WebSocket). Server-side only — it configures the proxy below, so it does
 * not need the NEXT_PUBLIC_ prefix.
 */
const mediaOrigin = process.env.SYNCAPP_MEDIA_ORIGIN ?? 'http://localhost:8080'

/**
 * The Content-Security-Policy is NOT here.
 *
 * It lives in `src/proxy.ts`, because a header declared in this file is a
 * constant and a constant policy cannot carry a nonce — which forced
 * `script-src 'unsafe-inline'`, the one token that made the whole policy
 * decorative. The per-request version mints a nonce instead. The headers below
 * are the ones that genuinely are the same for every response.
 */
const nextConfig: NextConfig = {
  reactCompiler: true,

  /**
   * Proxy the media endpoints through this app's own origin.
   *
   * The gateway sets no CORS headers anywhere, so a browser on a different
   * origin cannot PUT an upload to it: a cross-origin PUT is never a "simple"
   * request, the preflight OPTIONS hits no handler, and the browser blocks the
   * whole thing. Serving /media from our own origin sidesteps that without
   * touching the server. In production the app is typically served from the same
   * host as the gateway, where this rewrite is simply a no-op passthrough.
   */
  async rewrites() {
    return [
      {
        source: '/media/:path*',
        destination: `${mediaOrigin}/media/:path*`,
      },
    ]
  },

  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          // Defence in depth behind the CSP's frame-ancestors, for anything that
          // predates it.
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          // Chat content routinely includes links; a full Referer would leak the
          // page a user came from to whatever they click.
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          // The app asks for the microphone and camera for calls, and for nothing
          // else. Naming them keeps an injected iframe from asking on our behalf.
          {
            key: 'Permissions-Policy',
            value: 'camera=(self), microphone=(self), geolocation=(), payment=()',
          },
          { key: 'Cross-Origin-Opener-Policy', value: 'same-origin' },
        ],
      },
    ]
  },
}

export default nextConfig
