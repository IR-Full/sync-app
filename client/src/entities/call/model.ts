'use client'

import { create } from 'zustand'

import type { Wire } from '@/shared/api'

export type CallKind = 'audio' | 'video'
/** Room lifecycle — mirrors model.CallState on the server. */
export type CallRoomState = 'ringing' | 'active' | 'ended'
/** One user's status in the room — mirrors model.ParticipantState. */
export type ParticipantState = 'invited' | 'joined' | 'left' | 'declined'

export interface CallParticipant {
  userId: string
  deviceId: string
  state: ParticipantState
}

export interface CallRoom {
  callId: string
  chatId: string
  initiatorId: string
  kind: CallKind
  state: CallRoomState
  participants: CallParticipant[]
}

/** A peer is addressed by user AND device: one account can join from several. */
export function peerKey(userId: string, deviceId: string): string {
  return `${userId}:${deviceId}`
}

export function roomFromWire(state: Wire.CallState): CallRoom {
  return {
    callId: state.callId,
    chatId: state.chatId,
    initiatorId: state.initiatorId,
    kind: (state.kind === 'video' ? 'video' : 'audio') as CallKind,
    state: state.state as CallRoomState,
    participants: (state.participants ?? []).map((participant) => ({
      userId: participant.userId,
      deviceId: participant.deviceId,
      state: participant.state as ParticipantState,
    })),
  }
}

interface CallState {
  /** the room we are in or being rung for; only one at a time */
  room: CallRoom | null
  /** true once we have accepted (or started) and media is flowing */
  joined: boolean
  localStream: MediaStream | null
  /** peerKey -> that peer's inbound media */
  remoteStreams: Record<string, MediaStream>
  micMuted: boolean
  cameraOff: boolean
  /** last failure, surfaced in the call UI */
  error: string | null

  setRoom: (room: CallRoom | null) => void
  setJoined: (joined: boolean) => void
  setLocalStream: (stream: MediaStream | null) => void
  setRemoteStream: (key: string, stream: MediaStream | null) => void
  setMicMuted: (muted: boolean) => void
  setCameraOff: (off: boolean) => void
  setError: (error: string | null) => void
  reset: () => void
}

/**
 * The single active call.
 *
 * Zustand rather than the query cache: none of this is server-fetched state
 * with a request/response shape. The room comes from pushes, and the streams are
 * live browser objects that must never be serialised, cached or replayed.
 */
export const useCallStore = create<CallState>((set, get) => ({
  room: null,
  joined: false,
  localStream: null,
  remoteStreams: {},
  micMuted: false,
  cameraOff: false,
  error: null,

  setRoom: (room) => set({ room }),
  setJoined: (joined) => set({ joined }),
  setLocalStream: (localStream) => set({ localStream }),

  setRemoteStream: (key, stream) =>
    set((state) => {
      const remoteStreams = { ...state.remoteStreams }
      if (stream) remoteStreams[key] = stream
      else delete remoteStreams[key]
      return { remoteStreams }
    }),

  setMicMuted: (micMuted) => {
    // Muting has to reach the actual track, or the peer keeps hearing us.
    get()
      .localStream?.getAudioTracks()
      .forEach((track) => {
        track.enabled = !micMuted
      })
    set({ micMuted })
  },

  setCameraOff: (cameraOff) => {
    get()
      .localStream?.getVideoTracks()
      .forEach((track) => {
        track.enabled = !cameraOff
      })
    set({ cameraOff })
  },

  setError: (error) => set({ error }),

  /**
   * Tears the call down but deliberately leaves `error` alone.
   *
   * A failed call ends with reset() *and* an error to show, and the two race:
   * hanging up makes the server answer with an "ended" room, which resets again
   * a moment later. Clearing the message here would wipe it before anyone read
   * it, so dismissing an error is always an explicit `setError(null)`.
   */
  reset: () => {
    // Releasing the device is what turns the camera light off; forgetting it is
    // the classic WebRTC bug.
    get()
      .localStream?.getTracks()
      .forEach((track) => track.stop())
    set({
      room: null,
      joined: false,
      localStream: null,
      remoteStreams: {},
      micMuted: false,
      cameraOff: false,
    })
  },
}))

/** True when we are being rung and have not answered yet. */
export function isIncomingRing(
  room: CallRoom | null,
  selfId: string,
  joined: boolean,
): boolean {
  if (!room || joined) return false
  if (room.state === 'ended') return false
  if (room.initiatorId === selfId) return false
  const me = room.participants.find((participant) => participant.userId === selfId)
  return me?.state === 'invited'
}
