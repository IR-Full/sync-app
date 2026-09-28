import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Wire } from '@/shared/api'
import { isIncomingRing, peerKey, roomFromWire, useCallStore, type CallRoom } from './model'

/**
 * A MediaStream stand-in. jsdom has no WebRTC at all, and the store's job here is
 * precisely to reach through to the tracks — muting that never touches
 * `track.enabled` still leaves the peer hearing us, and a reset that never calls
 * `track.stop()` leaves the camera light on.
 */
function fakeStream(audio = 1, video = 1) {
  const track = (kind: string) => ({ kind, enabled: true, stop: vi.fn() })
  const audioTracks = Array.from({ length: audio }, () => track('audio'))
  const videoTracks = Array.from({ length: video }, () => track('video'))
  return {
    getAudioTracks: () => audioTracks,
    getVideoTracks: () => videoTracks,
    getTracks: () => [...audioTracks, ...videoTracks],
    audioTracks,
    videoTracks,
  }
}

const wire = (overrides: Partial<Wire.CallState> = {}): Wire.CallState =>
  ({
    callId: 'call-1',
    chatId: 'c1',
    initiatorId: 'u2',
    kind: 'video',
    state: 'ringing',
    participants: [
      { userId: 'u2', deviceId: 'd2', state: 'joined' },
      { userId: 'u1', deviceId: 'd1', state: 'invited' },
    ],
    ...overrides,
  }) as Wire.CallState

const room = (overrides: Partial<CallRoom> = {}): CallRoom => ({
  ...roomFromWire(wire()),
  ...overrides,
})

const store = () => useCallStore.getState()

beforeEach(() => {
  useCallStore.setState({
    room: null,
    joined: false,
    localStream: null,
    remoteStreams: {},
    micMuted: false,
    cameraOff: false,
    error: null,
  })
})

describe('peerKey', () => {
  it('combines user and device', () => {
    expect(peerKey('u1', 'd1')).toBe('u1:d1')
  })

  it('distinguishes two devices of one account', () => {
    // One user can join from a phone and a laptop at once; keying on the user id
    // alone would make the second stream replace the first.
    expect(peerKey('u1', 'd1')).not.toBe(peerKey('u1', 'd2'))
  })

  it('distinguishes two accounts', () => {
    expect(peerKey('u1', 'd1')).not.toBe(peerKey('u2', 'd1'))
  })
})

describe('roomFromWire', () => {
  it('maps the room across', () => {
    expect(roomFromWire(wire())).toMatchObject({
      callId: 'call-1',
      chatId: 'c1',
      initiatorId: 'u2',
      kind: 'video',
      state: 'ringing',
    })
  })

  it('maps the participant roster', () => {
    expect(roomFromWire(wire()).participants).toEqual([
      { userId: 'u2', deviceId: 'd2', state: 'joined' },
      { userId: 'u1', deviceId: 'd1', state: 'invited' },
    ])
  })

  it('defaults an absent roster to an empty array', () => {
    expect(roomFromWire(wire({ participants: undefined })).participants).toEqual([])
  })

  it('treats any kind other than video as audio', () => {
    // A newer server could introduce a kind this build has never heard of;
    // falling back to audio degrades to a working call rather than a blank one.
    expect(roomFromWire(wire({ kind: 'audio' })).kind).toBe('audio')
    expect(roomFromWire(wire({ kind: 'screenshare' })).kind).toBe('audio')
    expect(roomFromWire(wire({ kind: '' })).kind).toBe('audio')
  })

  it('keeps video as video', () => {
    expect(roomFromWire(wire({ kind: 'video' })).kind).toBe('video')
  })
})

describe('remote streams', () => {
  it('stores a stream under its peer key', () => {
    const stream = fakeStream() as unknown as MediaStream
    store().setRemoteStream('u2:d2', stream)
    expect(store().remoteStreams['u2:d2']).toBe(stream)
  })

  it('removes the entry when the stream is null', () => {
    // A peer leaving must take its tile with it; an entry holding null would
    // render an empty video element instead.
    store().setRemoteStream('u2:d2', fakeStream() as unknown as MediaStream)
    store().setRemoteStream('u2:d2', null)

    expect(store().remoteStreams).toEqual({})
  })

  it('keeps several peers at once', () => {
    store().setRemoteStream('u2:d2', fakeStream() as unknown as MediaStream)
    store().setRemoteStream('u3:d3', fakeStream() as unknown as MediaStream)
    expect(Object.keys(store().remoteStreams).sort()).toEqual(['u2:d2', 'u3:d3'])
  })

  it('removing one peer leaves the others', () => {
    store().setRemoteStream('u2:d2', fakeStream() as unknown as MediaStream)
    store().setRemoteStream('u3:d3', fakeStream() as unknown as MediaStream)
    store().setRemoteStream('u2:d2', null)

    expect(Object.keys(store().remoteStreams)).toEqual(['u3:d3'])
  })

  it('tolerates removing a peer that was never added', () => {
    expect(() => store().setRemoteStream('ghost', null)).not.toThrow()
  })
})

describe('mute controls', () => {
  it('disables the audio tracks when muting', () => {
    // Setting only the flag leaves the peer hearing us — the mute button would
    // look like it worked and do nothing.
    const stream = fakeStream()
    store().setLocalStream(stream as unknown as MediaStream)
    store().setMicMuted(true)

    expect(stream.audioTracks.every((track) => track.enabled === false)).toBe(true)
    expect(store().micMuted).toBe(true)
  })

  it('re-enables the audio tracks when unmuting', () => {
    const stream = fakeStream()
    store().setLocalStream(stream as unknown as MediaStream)
    store().setMicMuted(true)
    store().setMicMuted(false)

    expect(stream.audioTracks.every((track) => track.enabled === true)).toBe(true)
  })

  it('does not touch video when muting the mic', () => {
    const stream = fakeStream()
    store().setLocalStream(stream as unknown as MediaStream)
    store().setMicMuted(true)

    expect(stream.videoTracks.every((track) => track.enabled === true)).toBe(true)
  })

  it('disables the video tracks when the camera goes off', () => {
    const stream = fakeStream()
    store().setLocalStream(stream as unknown as MediaStream)
    store().setCameraOff(true)

    expect(stream.videoTracks.every((track) => track.enabled === false)).toBe(true)
    expect(store().cameraOff).toBe(true)
  })

  it('does not touch audio when the camera goes off', () => {
    const stream = fakeStream()
    store().setLocalStream(stream as unknown as MediaStream)
    store().setCameraOff(true)

    expect(stream.audioTracks.every((track) => track.enabled === true)).toBe(true)
  })

  it('records the flag even with no stream yet', () => {
    // The user can arm mute on the ring screen, before getUserMedia resolves.
    expect(() => store().setMicMuted(true)).not.toThrow()
    expect(store().micMuted).toBe(true)
  })

  it('mutes every track of a multi-track stream', () => {
    const stream = fakeStream(2, 2)
    store().setLocalStream(stream as unknown as MediaStream)
    store().setMicMuted(true)

    expect(stream.audioTracks.map((track) => track.enabled)).toEqual([false, false])
  })
})

describe('reset', () => {
  it('stops every local track so the camera light goes out', () => {
    // Forgetting this is the classic WebRTC leak: the call ends, the UI closes,
    // and the device stays held until the tab is killed.
    const stream = fakeStream()
    store().setLocalStream(stream as unknown as MediaStream)
    store().reset()

    expect(stream.getTracks().every((track) => track.stop.mock.calls.length === 1)).toBe(true)
  })

  it('clears the room, streams and toggles', () => {
    store().setRoom(room())
    store().setJoined(true)
    store().setLocalStream(fakeStream() as unknown as MediaStream)
    store().setRemoteStream('u2:d2', fakeStream() as unknown as MediaStream)
    store().setMicMuted(true)
    store().setCameraOff(true)

    store().reset()

    expect(store()).toMatchObject({
      room: null,
      joined: false,
      localStream: null,
      remoteStreams: {},
      micMuted: false,
      cameraOff: false,
    })
  })

  /**
   * A failed call ends with reset() *and* an error to show, and the two race:
   * hanging up makes the server answer with an "ended" room, which resets again
   * a moment later. Clearing the message here would wipe it before it was read.
   */
  it('deliberately leaves the error in place', () => {
    store().setError('camera unavailable')
    store().reset()
    expect(store().error).toBe('camera unavailable')
  })

  it('leaves the error in place across a second reset', () => {
    store().setError('camera unavailable')
    store().reset()
    store().reset()
    expect(store().error).toBe('camera unavailable')
  })

  it('is safe with no stream', () => {
    expect(() => store().reset()).not.toThrow()
  })
})

describe('setError', () => {
  it('records and clears a message', () => {
    store().setError('nope')
    expect(store().error).toBe('nope')
    store().setError(null)
    expect(store().error).toBeNull()
  })
})

describe('isIncomingRing', () => {
  /**
   * Decides whether the full-screen ring UI appears. A false positive rings the
   * caller's own device; a false negative means an incoming call arrives
   * silently.
   */
  it('is true when someone else is calling and we have not answered', () => {
    expect(isIncomingRing(room(), 'u1', false)).toBe(true)
  })

  it('is false once we have joined', () => {
    expect(isIncomingRing(room(), 'u1', true)).toBe(false)
  })

  it('is false for the caller', () => {
    // Otherwise placing a call would immediately ring your own screen.
    expect(isIncomingRing(room(), 'u2', false)).toBe(false)
  })

  it('is false with no room', () => {
    expect(isIncomingRing(null, 'u1', false)).toBe(false)
  })

  it('is false once the room has ended', () => {
    // The hangup broadcast arrives as an "ended" room; ringing on it would keep
    // the UI up after everyone left.
    expect(isIncomingRing(room({ state: 'ended' }), 'u1', false)).toBe(false)
  })

  it('is false when we are not on the roster at all', () => {
    expect(
      isIncomingRing(
        room({ participants: [{ userId: 'u2', deviceId: 'd2', state: 'joined' }] }),
        'u1',
        false,
      ),
    ).toBe(false)
  })

  it('is false once we have declined', () => {
    // Declining on one device must stop the ring, not restart it on the next
    // CALL_STATE push.
    expect(
      isIncomingRing(
        room({
          participants: [
            { userId: 'u2', deviceId: 'd2', state: 'joined' },
            { userId: 'u1', deviceId: 'd1', state: 'declined' },
          ],
        }),
        'u1',
        false,
      ),
    ).toBe(false)
  })

  it('is false once we have left', () => {
    expect(
      isIncomingRing(
        room({
          participants: [
            { userId: 'u2', deviceId: 'd2', state: 'joined' },
            { userId: 'u1', deviceId: 'd1', state: 'left' },
          ],
        }),
        'u1',
        false,
      ),
    ).toBe(false)
  })

  it('still rings for an active room we were invited to', () => {
    // Joining an ongoing group call is a real case: the room is 'active' because
    // others are already talking, but we have not answered.
    expect(isIncomingRing(room({ state: 'active' }), 'u1', false)).toBe(true)
  })
})
