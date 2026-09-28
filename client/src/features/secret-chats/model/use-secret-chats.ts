'use client'

import { useCallback, useEffect } from 'react'

import { useSecretStore, useTrustStore, type SecretMessage } from '@/entities/secret-chat'
import { useSessionStore } from '@/entities/session'
import { MsgType, useIsConnected, useSyncAppClient, type Wire } from '@/shared/api'
import { fromBase64, fromUtf8, toBase64, toUtf8 } from '@/shared/lib/e2e/codec'
import { generateKeyPair, signPreKey } from '@/shared/lib/e2e/keys'
import { marshalHeader, RatchetSession } from '@/shared/lib/e2e/ratchet'
import { x3dhInitiator } from '@/shared/lib/e2e/x3dh'
import { createDedupKey } from '@/shared/lib/id'

import { openSecretMessage } from './decrypt'

function sessionKey(userId: string, deviceId: string): string {
  return `${userId}:${deviceId}`
}

/**
 * Builds a SECRET_SEND body with the BINARY payload fields.
 *
 * The protocol carries the ratchet payload two ways: legacy text (a JSON header
 * plus a base64 ciphertext) and raw bytes. This client advertises CapSecretQueue,
 * which is the bit the gateway reads to decide what a peer can handle — so it sends
 * bytes, and the server re-encodes for any peer that cannot.
 *
 * That saves a third of every secret message. base64 inflates by 4/3, and the
 * ciphertext is the bulk of the frame; the header stays JSON-shaped because the
 * RECEIVING CLIENT parses it, so its bytes are the UTF-8 of that JSON rather than a
 * decoded blob.
 *
 * Only ONE form is populated. Sending both would double the payload, which is the
 * opposite of the point.
 */
function secretPayload(
  bundle: { userId?: string; deviceId: string },
  peerUserId: string,
  envelope: { ik?: string; ek?: string; rh: string },
  ciphertext: Uint8Array,
): Wire.SecretMsg {
  return {
    toUserId: bundle.userId || peerUserId,
    toDeviceId: bundle.deviceId,
    ratchetHeaderBin: toBase64(toUtf8(JSON.stringify(envelope))),
    ciphertextBin: toBase64(ciphertext),
  } as Wire.SecretMsg
}

/**
 * Reads the ratchet payload from a SECRET_RECV, whichever form it arrived in.
 *
 * Both have to be handled. The binary fields are what a current server sends this
 * client; the text ones are what an older one sends, and what arrives during a
 * rolling deploy. Preferring binary when both are set is the same rule the server
 * applies, so the two ends cannot disagree about which payload a frame carries.
 *
 * `bytes` fields cross the protobuf codec as base64 STRINGS (the codec decodes with
 * that setting), so the binary branch still decodes one — the saving is on the wire,
 * between the server and here, not in this function.
 */
function readSecretPayload(body: Wire.SecretMsg): {
  ratchetHeader: string
  ciphertext: string
} {
  const bin = body.ratchetHeaderBin ?? ''
  const cipherBin = body.ciphertextBin ?? ''
  if (bin || cipherBin) {
    return {
      // The header is TEXT (a JSON object) that travelled as bytes, so it decodes
      // back to the string the caller parses.
      ratchetHeader: fromUtf8(fromBase64(bin)),
      // The ciphertext is opaque and stays base64 for the decryptor, which decodes
      // it itself.
      ciphertext: cipherBin,
    }
  }
  return { ratchetHeader: body.ratchetHeader ?? '', ciphertext: body.ciphertext ?? '' }
}

/**
 * A peer device's long-term identity no longer matches what was pinned for it.
 *
 * Thrown rather than logged because the caller has to stop: continuing would
 * encrypt to whatever key the directory just supplied, which is the attack the
 * pin exists to catch. It carries the device so the UI can show that device's
 * safety number.
 */
export class IdentityChangedError extends Error {
  constructor(
    readonly userId: string,
    readonly deviceId: string,
  ) {
    super('e2e: the peer device identity changed')
    this.name = 'IdentityChangedError'
  }
}

/**
 * Maintains and publishes this device's prekey bundle.
 *
 * Three jobs that have to happen together. `maintainKeys` rotates the signed prekey
 * once a week and refills the one-time pool when the LOCAL count runs low; the
 * publish then sends whatever the directory has not confirmed; and the directory's
 * reply says what it actually holds, which is the only place that can come from.
 *
 * The reply is the part that was missing, and the gap it left was invisible from
 * both ends. One-time prekeys are consumed by PEERS fetching bundles, so the local
 * count cannot track them: it falls only when a message decrypts with a key, which
 * misses every fetch that never became a message. The directory would empty while
 * this device believed it was full, and X3DH would quietly drop to three
 * Diffie-Hellmans for every session started afterwards - weaker, and reported to
 * nobody, since the peer cannot tell the difference and the owner is never told.
 *
 * So KEY_PUBLISH is a REQUEST now, not fire-and-forget. `applyDirectoryState` folds
 * the answer back in and says whether the bundle has to go out again; when it does,
 * it clears `published` and this effect re-runs with the new keys.
 */
export function useSecretKeyPublisher(): void {
  const client = useSyncAppClient()
  const connected = useIsConnected()
  const userId = useSessionStore((state) => state.session?.userId ?? '')
  const published = useSecretStore((state) => state.published)

  useEffect(() => {
    if (!connected || !userId) return
    let cancelled = false

    // Hydration reads the vault, which is asynchronous. Nothing below may run
    // before it finishes: publishing a freshly generated identity while the
    // stored one is still being decrypted would replace the directory's copy
    // with keys no peer has a session for.
    void useSecretStore
      .getState()
      .hydrate(userId)
      .then(async (identity) => {
        if (cancelled) return

        const store = useSecretStore.getState()
        // Runs before the `published` check: a bundle published an hour ago is
        // still "published", and the key inside it can still have aged out since.
        if (store.maintainKeys()) {
          // maintainKeys cleared `published`; this effect re-runs with fresh keys
          // rather than publishing the ones captured above.
          return
        }
        if (published) return

        const current = useSecretStore.getState().identity ?? identity
        // Only what the directory has not acknowledged. Publishing is additive on
        // the server - it appends rather than replaces - so sending the whole pool
        // on every connect filed each public key again, and a one-time prekey
        // stored twice is one that can be handed out twice.
        const prekeys = useSecretStore
          .getState()
          .unpublishedPreKeys()
          .map((pair) => toBase64(pair.publicKey))

        let reply: Wire.KeyState
        try {
          const envelope = await client.request<Wire.KeyState>(
            MsgType.KEY_PUBLISH,
            {
              identityKey: toBase64(current.identity.publicKey),
              signingKey: toBase64(current.signing.publicKey),
              signedPrekey: toBase64(current.signedPreKey.publicKey),
              signedPrekeySig: toBase64(
                // Signed with the Ed25519 identity key so peers can prove the
                // prekey really came from us - the MITM defence against a hostile
                // directory.
                signPreKey(current.signing.privateKey, current.signedPreKey.publicKey),
              ),
              prekeys,
            },
            { expect: MsgType.KEY_STATE },
          )
          reply = envelope.body
        } catch {
          // Either the connection went away or the server is old enough not to
          // answer. `published` stays false, so the next connect republishes; the
          // directory upserts, so a repeat costs nothing.
          return
        }
        if (cancelled) return

        // An old server that ignores the request still leaves us better off than
        // before: the local maintenance above is unchanged, and the state below is
        // simply never applied.
        useSecretStore.getState().applyDirectoryState(reply, prekeys)
      })

    return () => {
      cancelled = true
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
  const client = useSyncAppClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  useEffect(() => {
    if (!selfId) return

    // Inbound messages are processed one at a time, in arrival order, on this
    // chain. Hydration is asynchronous, so the handler has to be able to wait —
    // and two handlers waiting in parallel would each read the same ratchet
    // state and then each overwrite it, which breaks the chain for every
    // message after them. A queue costs nothing here and removes the race.
    let queue: Promise<void> = Promise.resolve()

    return client.on('secret', (message) => {
      queue = queue.then(async () => {
        const identity = await useSecretStore.getState().hydrate(selfId)
        const store = useSecretStore.getState()

        const key = sessionKey(message.fromUserId, message.fromDeviceId)
        // Normalised first, because the payload arrives in one of two shapes and
        // the decryptor takes one. Which shape depends on what the SERVER decided
        // for this connection, not on anything visible here.
        const opened = openSecretMessage(identity, store.sessions[key], {
          ...message,
          ...readSecretPayload(message),
        })

        store.appendMessage({
          id: `${message.fromDeviceId}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          peerId: message.fromUserId,
          text: opened?.plaintext ?? '',
          timestamp: Date.now(),
          outgoing: false,
          failed: !opened,
        } satisfies SecretMessage)

        // A REPLAYED envelope carries a queue id, and acknowledging it is what lets
        // the server delete the row. Sent after the transcript write above, which is
        // the whole reason the queue is at-least-once: if this tab dies between the
        // two, the message is redelivered rather than lost.
        //
        // Sent even when decryption FAILED. A message whose ratchet key is gone will
        // never open no matter how often it is redelivered, and leaving it queued
        // means every future sync starts by failing on the same envelope — the
        // transcript already records the failure, which is what the user sees.
        if (message.queueId) {
          client.send(MsgType.SECRET_ACKED, { ids: [message.queueId] })
        }

        if (!opened) return
        store.saveSession(key, opened.session)
        if (opened.consumedOneTimePreKey) {
          store.dropOneTimePreKey(opened.consumedOneTimePreKey)
        }
      })
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
  const client = useSyncAppClient()
  const connected = useIsConnected()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')

  const send = useCallback(
    async (text: string) => {
      const trimmed = text.trim()
      if (!trimmed || !connected || !peerUserId) return

      const store = useSecretStore.getState()
      const identity = await store.hydrate(selfId)

      const bundles = await client.request<Wire.KeyBundles>(
        MsgType.KEY_FETCH_ALL,
        { userId: peerUserId, deviceId: '' },
        { expect: MsgType.KEY_BUNDLES },
      )
      if (!bundles.body.bundles.length) {
        throw new Error('no-devices')
      }

      const trust = useTrustStore.getState()
      for (const bundle of bundles.body.bundles) {
        const deviceUserId = bundle.userId || peerUserId
        const key = sessionKey(deviceUserId, bundle.deviceId)

        // Check the pin BEFORE any key material from this bundle is used. A
        // directory that swapped the identity key is caught here or not at all:
        // once the shared secret is derived the ciphertext is already readable
        // by whoever supplied the key.
        const verdict = trust.verify(
          deviceUserId,
          bundle.deviceId,
          bundle.identityKey,
          bundle.signingKey,
        )
        if (verdict.kind === 'changed') {
          // Refuse rather than warn-and-send. A reinstall and an attack look
          // identical from here, and the difference is exactly what the message
          // is worth protecting from — so the human decides, through the panel
          // that shows the safety number, and the send is retried afterwards.
          throw new IdentityChangedError(deviceUserId, bundle.deviceId)
        }

        // Re-read rather than using the snapshot taken before KEY_FETCH_ALL:
        // an inbound message decrypted while that request was in flight has
        // already advanced this session, and encrypting from the older state
        // would send under a chain key the peer has moved past.
        const stored = useSecretStore.getState().sessions[key]

        let session: RatchetSession
        let envelope: { ik?: string; ek?: string; rh: string }

        if (stored) {
          session = RatchetSession.deserialize(stored)
          const { header, ciphertext } = session.encrypt(toUtf8(trimmed))
          envelope = { rh: toBase64(marshalHeader(header)) }
          client.send(
            MsgType.SECRET_SEND,
            secretPayload(bundle, peerUserId, envelope, ciphertext),
          )
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
          client.send(
            MsgType.SECRET_SEND,
            secretPayload(bundle, peerUserId, envelope, ciphertext),
          )
        }

        store.saveSession(key, session.serialize())
        if (verdict.kind === 'first-use') {
          // Pin only after the session is established: pinning a key we then
          // failed to use would record an identity this device never actually
          // talked to.
          trust.accept(deviceUserId, bundle.deviceId, bundle.identityKey, bundle.signingKey)
        }
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
