import type { NextConfig } from 'next'

/**
 * Origin of the gateway's HTTP side (media upload/download lives there, not on
 * the WebSocket). Server-side only — it configures the proxy below, so it does
 * not need the NEXT_PUBLIC_ prefix.
 */
const mediaOrigin = process.env.SYNAPSE_MEDIA_ORIGIN ?? 'http://localhost:8080'

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
}

export default nextConfig
