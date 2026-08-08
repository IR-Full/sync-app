/**
 * Environment configuration.
 *
 * `NEXT_PUBLIC_*` values are inlined at build time, so each deployment target
 * (dev / stage / prod) gets its own build with its own gateway address. Nothing
 * here is hardcoded to localhost beyond the development default.
 */

export type AppEnv = 'development' | 'stage' | 'production'

function readEnv(): AppEnv {
  const value = process.env.NEXT_PUBLIC_APP_ENV
  if (value === 'stage' || value === 'production' || value === 'development') return value
  return process.env.NODE_ENV === 'production' ? 'production' : 'development'
}

/**
 * Falls back to the page's own origin so a build served from the same host as
 * the gateway needs no explicit URL — and so the scheme follows the page
 * (wss:// under https://), which is what a browser requires on a secure page.
 */
function resolveGatewayUrl(): string {
  const configured = process.env.NEXT_PUBLIC_SYNAPSE_WS_URL
  if (configured) return configured
  if (typeof window !== 'undefined') {
    const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${scheme}//${window.location.host}/ws`
  }
  return 'ws://localhost:8080/ws'
}

/**
 * ICE servers for WebRTC calls.
 *
 * The gateway relays signalling only — it is not a STUN/TURN server and never
 * sees media — so NAT traversal is entirely the client's problem. The default
 * is a public STUN server, which is enough to connect most peers but discloses
 * their IP to a third party and cannot traverse symmetric NAT. Point
 * `NEXT_PUBLIC_ICE_SERVERS` at your own STUN/TURN (JSON array of RTCIceServer)
 * for anything beyond a demo; set it to `[]` to use host candidates only, which
 * works on a LAN.
 */
function readIceServers(): RTCIceServer[] {
  const raw = process.env.NEXT_PUBLIC_ICE_SERVERS
  if (!raw) return [{ urls: 'stun:stun.l.google.com:19302' }]
  try {
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as RTCIceServer[]) : []
  } catch {
    console.warn('[synapse] NEXT_PUBLIC_ICE_SERVERS is not valid JSON; falling back to none')
    return []
  }
}

export const appEnv: AppEnv = readEnv()

export const config = {
  env: appEnv,
  isProduction: appEnv === 'production',
  /** WebSocket endpoint of the Synapse gateway (`/ws` in cmd/server/main.go). */
  get gatewayUrl(): string {
    return resolveGatewayUrl()
  },
  /** Sent in HELLO; shows up in the gateway's connection logs. */
  clientVersion: process.env.NEXT_PUBLIC_CLIENT_VERSION ?? 'web/0.1',
  /** How many messages one history page requests (the gateway caps this at 100). */
  historyPageSize: 40,
  /** How long a typing indicator stays visible after the last TYPING frame. */
  typingTimeoutMs: 4000,
  /** Minimum gap between TYPING frames — the gateway throttles to ~1 per 2s per chat. */
  typingThrottleMs: 2500,
  /** STUN/TURN used to negotiate call media paths. */
  iceServers: readIceServers(),
} as const
