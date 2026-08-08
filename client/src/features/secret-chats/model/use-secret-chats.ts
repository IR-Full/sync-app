'use client'

import { useCallback, useEffect } from 'react'

import { useSecretStore, type SecretMessage } from '@/entities/secret-chat'
import { useSessionStore } from '@/entities/session'
import { MsgType, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'
import { fromBase64, toBase64, toUtf8 } from '@/shared/lib/e2e/codec'
import { generateKeyPair, signPreKey } from '@/shared/lib/e2e/keys'
import { marshalHeader, RatchetSession } from '@/shared/lib/e2e/ratchet'
import { x3dhInitiator } from '@/shared/lib/e2e/x3dh'
import { createDedupKey } from '@/shared/lib/id'

import { openSecretMessage } from './decrypt'

function sessionKey(userId: string, deviceId: string): string {
  return `${userId}:${deviceId}`
}

/**
 * Publishes this device's prekey bundle once per connection.
 *
 * KEY_PUBLISH is fire-and-forget — the protocol gives the publisher no ack — so
 * "published" is tracked locally and simply redone on the next connect. The
 * directory upserts, so republishing is harmless.
 */
export function useSecretKeyPublisher(): void {
  const client = useSynapseClient()
  const connected = useIsConnected()
  const userId = useSessionStore((state) => state.session?.userId ?? '')
  const published = useSecretStore((state) => state.published)

  useEffect(() => {
    if (!connected || !userId || published) return

    const identity =
      useSecretStore.getState().identity ?? useSecretStore.getState().load(userId)
    try {
      client.send(MsgType.KEY_PUBLISH, {
        identityKey: toBase64(identity.identity.publicKey),
        signingKey: toBase64(identity.signing.publicKey),
        signedPrekey: toBase64(identity.signedPreKey.publicKey),
        signedPrekeySig: toBase64(
          // Signed with the Ed25519 identity key so peers can prove the prekey
          // really came from us — the MITM defence against a hostile directory.
          signPreKey(identity.signing.privateKey, identity.signedPreKey.publicKey),
        ),
        prekeys: identity.oneTimePreKeys.map((pair) => toBase64(pair.publicKey)),
      })
      useSecretStore.getState().setPublished(true)
    } catch {
      // Not connected after all; the next state change retries.
    }
  }, [client, connected, userId, published])
}

/**
 * Receives and decrypts inbound secret messages.
 *
 * Mounted once at the app root: a secret message can arrive for any peer at any
 * time, and it must be decrypted when it lands — the ratchet state advances with
 * each message, so deferring the work would reorder the chain.
 */
export function useSecretChatEngine(): void {
  const client = useSynapseClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  useEffect(() => {
    if (!selfId) return

    return client.on('secret', (message) => {
      const store = useSecretStore.getState()
      const identity = store.identity
      if (!identity) return

      const key = sessionKey(message.fromUserId, message.fromDeviceId)
      const opened = openSecretMessage(identity, store.sessions[key], message)

      store.appendMessage({
        id: `${message.fromDeviceId}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        peerId: message.fromUserId,
        text: opened?.plaintext ?? '',
        timestamp: Date.now(),
        outgoing: false,
        failed: !opened,
      } satisfies SecretMessage)

      if (!opened) return
      store.saveSession(key, opened.session)
      if (opened.consumedOneTimePreKey) {
        store.dropOneTimePreKey(opened.consumedOneTimePreKey)
      }
    })
  }, [client, selfId])
}

/**
 * Sends secret messages to every device a peer has published keys for.
 *
 * Multi-device is the caller's job here: the relay addresses one device at a
 * time, so a message to a user is really N ciphertexts, each under its own
 * ratchet.
 */
export function useSecretChat(peerUserId: string) {
  const client = useSynapseClient()
  const connected = useIsConnected()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  const send = useCallback(
    async (text: string) => {
      const trimmed = text.trim()
      if (!trimmed || !connected || !peerUserId) return

      const store = useSecretStore.getState()
      const identity = store.identity ?? store.load(selfId)

      const bundles = await client.request<Wire.KeyBundles>(
        MsgType.KEY_FETCH_ALL,
        { userId: peerUserId, deviceId: '' },
        { expect: MsgType.KEY_BUNDLES },
      )
      if (!bundles.body.bundles.length) {
        throw new Error('no-devices')
      }

      for (const bundle of bundles.body.bundles) {
        const key = sessionKey(bundle.userId || peerUserId, bundle.deviceId)
        const stored = store.sessions[key]

        let session: RatchetSession
        let envelope: { ik?: string; ek?: string; rh: string }

        if (stored) {
          session = RatchetSession.deserialize(stored)
          const { header, ciphertext } = session.encrypt(toUtf8(trimmed))
          envelope = { rh: toBase64(marshalHeader(header)) }
          client.send(MsgType.SECRET_SEND, {
            toUserId: bundle.userId || peerUserId,
            toDeviceId: bundle.deviceId,
            ratchetHeader: JSON.stringify(envelope),
            ciphertext: toBase64(ciphertext),
          })
        } else {
          const ephemeral = generateKeyPair()
          const { sharedSecret, ephemeralPublicKey } = x3dhInitiator(
            { identity: identity.identity, ephemeral },
            {
              identityKey: fromBase64(bundle.identityKey),
              signingKey: fromBase64(bundle.signingKey),
              signedPreKey: fromBase64(bundle.signedPrekey),
              signedPreKeySig: fromBase64(bundle.signedPrekeySig),
              oneTimePreKey: fromBase64(bundle.oneTimePrekey),
            },
          )
          session = RatchetSession.initiator(sharedSecret, fromBase64(bundle.signedPrekey))
          const { header, ciphertext } = session.encrypt(toUtf8(trimmed))
          envelope = {
            ik: toBase64(identity.identity.publicKey),
            ek: toBase64(ephemeralPublicKey),
            rh: toBase64(marshalHeader(header)),
          }
          client.send(MsgType.SECRET_SEND, {
            toUserId: bundle.userId || peerUserId,
            toDeviceId: bundle.deviceId,
            ratchetHeader: JSON.stringify(envelope),
            ciphertext: toBase64(ciphertext),
          })
        }

        store.saveSession(key, session.serialize())
      }

      store.appendMessage({
        id: createDedupKey(),
        peerId: peerUserId,
        text: trimmed,
        timestamp: Date.now(),
        outgoing: true,
      })
    },
    [client, connected, peerUserId, selfId],
  )

  return { send }
}
