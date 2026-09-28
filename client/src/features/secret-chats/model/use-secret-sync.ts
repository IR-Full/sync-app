'use client'

import { useEffect, useRef } from 'react'

import { useSessionStore } from '@/entities/session'
import { MsgType, useIsConnected, useSyncAppClient, type Wire } from '@/shared/api'

/** Server-side cap on one sync page; asking for more returns this many. */
const PAGE_LIMIT = 100

/** Refuses to walk forever if the server ever returns a non-advancing cursor. */
const MAX_PAGES = 200

/**
 * Collects the secret messages that arrived while this device was away.
 *
 * Until the server grew a queue, there was nothing to collect: SECRET_SEND was a
 * pure relay, so a message sent while this tab was closed was published to zero
 * nodes and discarded — no store, no push, and no error to the sender, which drew
 * "sent" regardless. A secret conversation between two people who were not online
 * at the same moment exchanged nothing at all.
 *
 * So this hook is the client half of the fix, and it is not optional: without it the
 * queue fills, the sender is told "queued", and the messages sit on the server until
 * their TTL collects them.
 *
 * The frames come back as ordinary SECRET_RECV, which `useSecretChatEngine` already
 * handles — decrypting, appending to the transcript, and acknowledging by queue id.
 * That is why this hook does not decrypt anything: it asks, and the existing inbound
 * path does the work. Two places decrypting into the same ratchet would be two
 * places advancing it.
 */
export function useSecretSync(): void {
  const client = useSyncAppClient()
  const connected = useIsConnected()
  const userId = useSessionStore((state) => state.session?.userId ?? '')
  /** guards against a second run for the same connection */
  const syncedFor = useRef<string | null>(null)

  useEffect(() => {
    if (!connected || !userId) {
      // Allow a resync after a drop: messages may have queued up while offline,
      // which is exactly the case this exists for.
      syncedFor.current = null
      return
    }
    if (syncedFor.current === userId) return
    syncedFor.current = userId

    let cancelled = false

    void (async () => {
      let after = ''

      for (let page = 0; page < MAX_PAGES; page++) {
        let reply: { body: Wire.SecretSynced }
        try {
          reply = await client.request<Wire.SecretSynced>(
            MsgType.SECRET_SYNC,
            { after, limit: PAGE_LIMIT },
            { expect: MsgType.SECRET_SYNCED },
          )
        } catch {
          // An older gateway does not know this type, and a transient failure is not
          // worth surfacing: the queue keeps the messages, and the next connect tries
          // again. What must NOT happen is an error dialog about a sync the user never
          // asked for.
          return
        }
        if (cancelled) return

        // The envelopes themselves arrived as SECRET_RECV frames before this
        // terminator, and the engine has already decrypted and acknowledged them.
        // This body only says how many and whether there are more.
        if (reply.body.done || !reply.body.nextAfter || reply.body.nextAfter === after) return
        after = reply.body.nextAfter
      }
    })()

    return () => {
      cancelled = true
    }
  }, [client, connected, userId])
}
