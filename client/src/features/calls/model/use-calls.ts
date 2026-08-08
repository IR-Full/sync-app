'use client'

import { useCallback, useEffect, useRef } from 'react'

import { peerKey, roomFromWire, useCallStore, type CallKind } from '@/entities/call'
import { useSessionStore } from '@/entities/session'
import { MsgType, useSynapseClient, type Wire } from '@/shared/api'

import { CallSession, type OutboundSignal } from './webrtc'

/**
 * Turns a getUserMedia rejection into something a user can act on.
 *
 * The DOMException names are the only reliable signal here — the messages differ
 * per browser and are not localisable.
 */
function describeMediaError(error: unknown): string {
  if (error instanceof DOMException) {
    if (error.name === 'NotAllowedError' || error.name === 'SecurityError') {
      return 'permission-denied'
    }
    if (error.name === 'NotFoundError' || error.name === 'OverconstrainedError') {
      return 'no-device'
    }
  }
  return error instanceof Error ? error.message : 'call failed'
}

/** Media constraints for a call kind. Audio is always on; video only for video calls. */
function constraintsFor(kind: CallKind): MediaStreamConstraints {
  return {
    audio: true,
    video: kind === 'video' ? { width: { ideal: 1280 }, height: { ideal: 720 } } : false,
  }
}

/**
 * Drives one call from start to finish.
 *
 * Split of responsibility: the *server* owns the roster (who is invited, who
 * joined, when the room ends) and this hook owns the media. A CALL_STATE push is
 * the single trigger for reconciling peer connections, so the mesh always
 * follows the authoritative roster rather than a guess made at invite time.
 */
export function useCallEngine(): void {
  const client = useSynapseClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')
  const selfDeviceId = useSessionStore((state) => state.session?.deviceId ?? '')
  const session = useRef<CallSession | null>(null)
  /** signals that arrived before the media session existed */
  const pending = useRef<Wire.CallSignal[]>([])

  // --- inbound room state
  useEffect(() => {
    if (!selfId) return

    return client.on('callState', (state) => {
      const room = roomFromWire(state)
      const store = useCallStore.getState()

      // Ignore rooms we are not part of at all.
      const involved =
        room.initiatorId === selfId ||
        room.participants.some((participant) => participant.userId === selfId)
      if (!involved) return

      // A second concurrent room is not something this UI supports; the first
      // one wins until it ends.
      if (store.room && store.room.callId !== room.callId && store.room.state !== 'ended')
        return

      store.setRoom(room)

      if (room.state === 'ended') {
        session.current?.close()
        session.current = null
        pending.current = []
        store.reset()
        return
      }

      // Our own participant row disappearing (or going to left/declined) means
      // this device is out, even if the room continues without us.
      const me = room.participants.find(
        (participant) =>
          participant.userId === selfId &&
          (participant.deviceId === selfDeviceId || participant.deviceId === ''),
      )
      if (me && (me.state === 'left' || me.state === 'declined')) {
        session.current?.close()
        session.current = null
        store.reset()
        return
      }

      session.current?.syncPeers(room.participants)
    })
  }, [client, selfId, selfDeviceId])

  // --- inbound SDP/ICE
  useEffect(() => {
    if (!selfId) return

    return client.on('callSignal', (signal) => {
      const active = session.current
      if (!active) {
        // Buffer: the offer can beat our getUserMedia prompt.
        pending.current.push(signal)
        return
      }
      void active.handleSignal(signal)
    })
  }, [client, selfId])

  // Expose the session to the actions hook below without re-creating it.
  useEffect(() => {
    engineRef.session = session
    engineRef.pending = pending
    return () => {
      engineRef.session = null
      engineRef.pending = null
    }
  }, [])
}

/**
 * Shared handle onto the engine's session.
 *
 * A module-level ref rather than context: the engine is mounted exactly once at
 * the app root, and threading a provider through purely so the call buttons can
 * reach it would add a layer that carries no other value.
 */
const engineRef: {
  session: { current: CallSession | null } | null
  pending: { current: Wire.CallSignal[] } | null
} = { session: null, pending: null }

export function useCallActions() {
  const client = useSynapseClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')
  const selfDeviceId = useSessionStore((state) => state.session?.deviceId ?? '')

  const sendSignal = useCallback(
    (callId: string) => (signal: OutboundSignal) => {
      try {
        client.send(MsgType.CALL_SIGNAL, {
          callId,
          toUserId: signal.toUserId,
          toDeviceId: signal.toDeviceId,
          signalType: signal.signalType,
          payload: signal.payload,
        })
      } catch {
        // A dropped candidate is recoverable via ICE restart; a dropped offer is
        // retried by renegotiation.
      }
    },
    [client],
  )

  /** Acquires media and starts the mesh for a room we are now part of. */
  const startMedia = useCallback(
    async (callId: string, kind: CallKind) => {
      const store = useCallStore.getState()
      if (!engineRef.session) return

      const stream = await navigator.mediaDevices.getUserMedia(constraintsFor(kind))
      store.setLocalStream(stream)
      store.setJoined(true)

      const active = new CallSession({
        selfUserId: selfId,
        selfDeviceId,
        localStream: stream,
        sendSignal: sendSignal(callId),
        onRemoteStream: (key, remote) => useCallStore.getState().setRemoteStream(key, remote),
        onError: (message) => useCallStore.getState().setError(message),
      })
      engineRef.session.current = active

      const room = useCallStore.getState().room
      if (room) active.syncPeers(room.participants)

      // Replay anything that arrived while the permission prompt was open.
      const buffered = engineRef.pending?.current ?? []
      if (engineRef.pending) engineRef.pending.current = []
      for (const signal of buffered) void active.handleSignal(signal)
    },
    [selfId, selfDeviceId, sendSignal],
  )

  /** Starts (or joins) a call in a chat. */
  const startCall = useCallback(
    async (chatId: string, kind: CallKind) => {
      const store = useCallStore.getState()
      store.setError(null)
      try {
        const reply = await client.request<Wire.CallState>(
          MsgType.CALL_INVITE,
          { chatId, kind },
          { expect: MsgType.CALL_STATE },
        )
        const room = roomFromWire(reply.body)
        store.setRoom(room)
        await startMedia(room.callId, room.kind)
      } catch (error) {
        // The invite may already have created a ringing room server-side. If
        // media then fails (permission denied, no device), leaving it would keep
        // everyone else ringing for a call nobody can join — so tear it down.
        const created = useCallStore.getState().room
        if (created) {
          try {
            client.send(MsgType.CALL_HANGUP, { callId: created.callId })
          } catch {
            // The room times out on its own if this cannot be delivered.
          }
        }
        // reset() clears the error too, so the message is set after it.
        store.reset()
        useCallStore.getState().setError(describeMediaError(error))
      }
    },
    [client, startMedia],
  )

  const acceptCall = useCallback(async () => {
    const store = useCallStore.getState()
    const room = store.room
    if (!room) return
    store.setError(null)
    try {
      // Media first: accepting and then failing the permission prompt would
      // leave the room believing we are in it.
      const reply = await client.request<Wire.CallState>(
        MsgType.CALL_ACCEPT,
        { callId: room.callId },
        { expect: MsgType.CALL_STATE },
      )
      store.setRoom(roomFromWire(reply.body))
      await startMedia(room.callId, room.kind)
    } catch (error) {
      // We may have already told the room we joined; back out so the others do
      // not wait on a participant with no media.
      try {
        client.send(MsgType.CALL_HANGUP, { callId: room.callId })
      } catch {
        // Best effort — the server reaps the room regardless.
      }
      store.reset()
      useCallStore.getState().setError(describeMediaError(error))
    }
  }, [client, startMedia])

  const declineCall = useCallback(() => {
    const store = useCallStore.getState()
    const room = store.room
    if (!room) return
    try {
      client.send(MsgType.CALL_DECLINE, { callId: room.callId })
    } catch {
      // Nothing to recover: the room times out on its own.
    }
    engineRef.session?.current?.close()
    if (engineRef.session) engineRef.session.current = null
    store.reset()
  }, [client])

  const hangUp = useCallback(() => {
    const store = useCallStore.getState()
    const room = store.room
    if (!room) return
    try {
      client.send(MsgType.CALL_HANGUP, { callId: room.callId })
    } catch {
      // Same as decline: the server reaps the room regardless.
    }
    engineRef.session?.current?.close()
    if (engineRef.session) engineRef.session.current = null
    store.reset()
  }, [client])

  return { startCall, acceptCall, declineCall, hangUp }
}

/** Stable label for a remote tile. */
export function participantKey(userId: string, deviceId: string): string {
  return peerKey(userId, deviceId)
}
