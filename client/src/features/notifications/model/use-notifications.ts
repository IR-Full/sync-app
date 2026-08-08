'use client'

import { useEffect, useRef } from 'react'

import { useChatStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { useSettingsStore } from '@/entities/settings'
import { labelForUser, useUserDirectory } from '@/entities/user'
import { useSynapseClient } from '@/shared/api'

/**
 * Browser notifications for messages that arrive while the tab is hidden.
 *
 * Client-side by necessity: the server's push path needs a device token
 * registered with PUSH_TOKEN and a configured provider endpoint, which is a
 * native-app concern (FCM/APNs). A web client can only notify while it is
 * running, so that is exactly what this does — and it stays quiet when the tab
 * is focused, where the message is already visible.
 */
export function useMessageNotifications(): void {
  const client = useSynapseClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')
  const enabled = useSettingsStore((state) => state.desktopNotifications)
  const sound = useSettingsStore((state) => state.soundOnMessage)
  const lastNotifiedAt = useRef(0)

  useEffect(() => {
    if (!selfId || (!enabled && !sound)) return

    return client.on('message', (message) => {
      if (message.senderId === selfId || message.deleted) return
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') return

      // One alert per second at most: a backlog replayed after a reconnect must
      // not produce a burst of notifications.
      const now = Date.now()
      if (now - lastNotifiedAt.current < 1000) return
      lastNotifiedAt.current = now

      if (
        enabled &&
        typeof Notification !== 'undefined' &&
        Notification.permission === 'granted'
      ) {
        const chat = useChatStore.getState().chats[message.chatId]
        const sender = labelForUser(
          useUserDirectory.getState().users[message.senderId],
          message.senderId,
        )
        new Notification(chat?.title || sender, {
          body: message.text.slice(0, 140),
          tag: message.chatId,
        })
      }

      if (sound) playChime()
    })
  }, [client, selfId, enabled, sound])
}

/** Short tone via WebAudio — avoids shipping an audio asset for one blip. */
function playChime(): void {
  try {
    const AudioCtor =
      window.AudioContext ??
      (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!AudioCtor) return
    const context = new AudioCtor()
    const oscillator = context.createOscillator()
    const gain = context.createGain()
    oscillator.frequency.value = 880
    gain.gain.setValueAtTime(0.0001, context.currentTime)
    gain.gain.exponentialRampToValueAtTime(0.08, context.currentTime + 0.01)
    gain.gain.exponentialRampToValueAtTime(0.0001, context.currentTime + 0.25)
    oscillator.connect(gain).connect(context.destination)
    oscillator.start()
    oscillator.stop(context.currentTime + 0.26)
    oscillator.onended = () => void context.close()
  } catch {
    // Autoplay policy or no audio device — silence is an acceptable outcome.
  }
}
