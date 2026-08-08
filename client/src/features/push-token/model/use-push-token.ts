'use client'

import { useCallback, useState } from 'react'

import { MsgType, useIsConnected, useSynapseClient } from '@/shared/api'

/**
 * Registers this browser's Web Push subscription with the gateway.
 *
 * What the server does with it: `PUSH_TOKEN` stores an opaque string on the
 * device row, and the notify worker hands `{token, platform}` to whatever HTTP
 * push provider is configured. It never interprets the token, so a Web Push
 * subscription (endpoint + keys, as JSON) is a valid thing to register — but the
 * provider on the other side has to speak Web Push for a notification to
 * actually arrive. Without a configured provider this registration is inert,
 * which is why nothing here claims delivery.
 *
 * Requires a VAPID public key (`NEXT_PUBLIC_VAPID_PUBLIC_KEY`) and a service
 * worker; with neither, the hook reports why instead of failing quietly.
 */
export type PushStatus =
  'unsupported' | 'not-configured' | 'denied' | 'idle' | 'registering' | 'registered' | 'failed'

const VAPID_KEY = process.env.NEXT_PUBLIC_VAPID_PUBLIC_KEY ?? ''

/**
 * Why push cannot be set up at all, or null when it can be attempted.
 *
 * Derived during render rather than written into state from an effect: these are
 * facts about the environment, not the result of any work we do.
 */
function blockingReason(): PushStatus | null {
  if (typeof window === 'undefined') return null
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) return 'unsupported'
  if (!VAPID_KEY) return 'not-configured'
  if (typeof Notification !== 'undefined' && Notification.permission === 'denied')
    return 'denied'
  return null
}

/**
 * VAPID keys are distributed base64url; the Push API wants raw bytes.
 *
 * Returns an ArrayBuffer rather than a view: `applicationServerKey` is typed as
 * a `BufferSource` backed specifically by an ArrayBuffer, which a generic
 * Uint8Array does not satisfy.
 */
function vapidKeyToBytes(base64Url: string): ArrayBuffer {
  const padding = '='.repeat((4 - (base64Url.length % 4)) % 4)
  const base64 = (base64Url + padding).replace(/-/g, '+').replace(/_/g, '/')
  const binary = atob(base64)
  const buffer = new ArrayBuffer(binary.length)
  const bytes = new Uint8Array(buffer)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return buffer
}

export function usePushToken(): {
  status: PushStatus
  register: () => Promise<void>
  ready: boolean
} {
  const client = useSynapseClient()
  const connected = useIsConnected()
  const [attempt, setAttempt] = useState<PushStatus>('idle')

  const blocked = blockingReason()

  const register = useCallback(async () => {
    if (blockingReason()) return
    try {
      const registration = await navigator.serviceWorker.register('/push-sw.js')
      const subscription =
        (await registration.pushManager.getSubscription()) ??
        (await registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: vapidKeyToBytes(VAPID_KEY),
        }))

      client.send(MsgType.PUSH_TOKEN, { token: JSON.stringify(subscription.toJSON()) })
      setAttempt('registered')
    } catch {
      setAttempt('failed')
    }
  }, [client])

  // Deliberately not automatic. Subscribing a browser to push is a consequential,
  // user-visible act — it belongs behind an explicit button in settings rather
  // than happening as a side effect of connecting.
  return { status: blocked ?? attempt, register, ready: connected && !blocked }
}
