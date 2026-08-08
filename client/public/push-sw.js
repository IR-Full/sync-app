/**
 * Service worker for Web Push.
 *
 * Only registered when NEXT_PUBLIC_VAPID_PUBLIC_KEY is configured and the user
 * has enabled notifications. The payload shape is whatever the configured push
 * provider sends; this handles a plain JSON {title, body, tag} and falls back to
 * raw text so a differently-shaped payload still surfaces something.
 */
self.addEventListener('push', (event) => {
  let title = 'Synapse'
  let body = ''
  let tag

  if (event.data) {
    try {
      const payload = event.data.json()
      title = payload.title || title
      body = payload.body || payload.preview || ''
      tag = payload.tag || payload.chat_id
    } catch {
      body = event.data.text()
    }
  }

  event.waitUntil(self.registration.showNotification(title, { body, tag }))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      const existing = clients.find((client) => 'focus' in client)
      if (existing) return existing.focus()
      return self.clients.openWindow('/chats')
    }),
  )
})
