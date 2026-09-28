import { peerKey, type CallParticipant } from '@/entities/call'
import { config } from '@/shared/config/env'

/** What travels in a CALL_SIGNAL payload. The server never parses it. */
export type SignalKind = 'offer' | 'answer' | 'ice'

export interface OutboundSignal {
  toUserId: string
  toDeviceId: string
  signalType: SignalKind
  payload: string
}

export interface InboundSignal {
  fromUserId: string
  fromDeviceId: string
  signalType: string
  payload: string
}

interface Peer {
  userId: string
  deviceId: string
  connection: RTCPeerConnection
  /**
   * Perfect-negotiation role. The peer with the higher key is "polite": on a
   * collision it rolls back its own offer and accepts the other side's, so the
   * two never deadlock trading offers.
   */
  polite: boolean
  makingOffer: boolean
  ignoreOffer: boolean
  /** How many times ICE has been restarted for this peer. */
  iceRestarts: number
}

/**
 * How many ICE restarts to attempt before giving up on a peer.
 *
 * A restart can genuinely rescue a connection that lost its path — a network
 * handover, a NAT rebinding. What it cannot rescue is a peer pair that has no
 * traversable path at all, which is the ordinary outcome of symmetric NAT with
 * no TURN server (see `iceServers` in shared/config/env: the default is a public
 * STUN, and the gateway is explicitly not a relay). In that case `failed` is
 * terminal, and retrying it forever costs battery and data to learn nothing.
 */
const MAX_ICE_RESTARTS = 3

/** Backoff between restarts, so three attempts are not three in one second. */
const ICE_RESTART_DELAY_MS = 2_000

export interface CallSessionOptions {
  selfUserId: string
  selfDeviceId: string
  localStream: MediaStream
  sendSignal: (signal: OutboundSignal) => void
  onRemoteStream: (key: string, stream: MediaStream | null) => void
  onError: (message: string) => void
}

/**
 * A mesh of peer connections, one per remote participant device.
 *
 * The server relays signalling and nothing else — it never sees media and never
 * parses SDP. That means every pair of participants negotiates directly here,
 * which is fine for the small rooms this client targets; a large conference
 * would need an SFU, which the server explicitly does not provide.
 *
 * Two problems this class exists to solve:
 *
 *  - **Glare.** Both sides can decide to offer at the same instant. The
 *    "perfect negotiation" pattern resolves it deterministically by role
 *    (derived from comparing peer keys) rather than by luck.
 *  - **Roster churn.** Participants join and leave at arbitrary times, so peers
 *    are reconciled against each CALL_STATE rather than created once.
 */
export class CallSession {
  private readonly peers = new Map<string, Peer>()
  private readonly options: CallSessionOptions
  private closed = false

  constructor(options: CallSessionOptions) {
    this.options = options
  }

  private get selfKey(): string {
    return peerKey(this.options.selfUserId, this.options.selfDeviceId)
  }

  /**
   * Brings the peer set in line with the roster: opens connections to newly
   * joined devices and tears down the ones that left.
   */
  syncPeers(participants: CallParticipant[]): void {
    if (this.closed) return

    const wanted = new Set<string>()
    for (const participant of participants) {
      if (participant.state !== 'joined') continue
      const key = peerKey(participant.userId, participant.deviceId)
      // Our own other devices are peers too, but this very device is not.
      if (key === this.selfKey) continue
      wanted.add(key)
      if (!this.peers.has(key)) {
        this.openPeer(participant.userId, participant.deviceId, key)
      }
    }

    for (const key of [...this.peers.keys()]) {
      if (!wanted.has(key)) this.closePeer(key)
    }
  }

  private openPeer(userId: string, deviceId: string, key: string): void {
    const connection = new RTCPeerConnection({ iceServers: config.iceServers })
    const peer: Peer = {
      userId,
      deviceId,
      connection,
      polite: this.selfKey > key,
      makingOffer: false,
      ignoreOffer: false,
      iceRestarts: 0,
    }
    this.peers.set(key, peer)

    for (const track of this.options.localStream.getTracks()) {
      connection.addTrack(track, this.options.localStream)
    }

    connection.ontrack = (event) => {
      this.options.onRemoteStream(key, event.streams[0] ?? null)
    }

    connection.onicecandidate = (event) => {
      if (!event.candidate) return
      this.options.sendSignal({
        toUserId: userId,
        toDeviceId: deviceId,
        signalType: 'ice',
        payload: JSON.stringify(event.candidate.toJSON()),
      })
    }

    connection.onnegotiationneeded = async () => {
      try {
        peer.makingOffer = true
        await connection.setLocalDescription()
        if (!connection.localDescription) return
        this.options.sendSignal({
          toUserId: userId,
          toDeviceId: deviceId,
          signalType: 'offer',
          payload: JSON.stringify(connection.localDescription),
        })
      } catch (error) {
        this.options.onError(error instanceof Error ? error.message : 'negotiation failed')
      } finally {
        peer.makingOffer = false
      }
    }

    connection.onconnectionstatechange = () => {
      if (connection.connectionState !== 'failed') return
      if (this.closed) return

      // A failed transport can sometimes be salvaged by restarting ICE rather
      // than rebuilding the whole peer — but only sometimes, and the bounded
      // retry is what separates "recovering from a handover" from "hammering a
      // path that does not exist".
      if (peer.iceRestarts >= MAX_ICE_RESTARTS) {
        this.options.onError(
          `Could not establish a connection to this participant after ${MAX_ICE_RESTARTS} attempts`,
        )
        this.closePeer(key)
        return
      }

      peer.iceRestarts += 1
      const attempt = peer.iceRestarts
      setTimeout(() => {
        // The peer may have left, or the call ended, during the wait.
        if (this.closed || this.peers.get(key) !== peer) return
        if (connection.connectionState !== 'failed') return
        connection.restartIce()
      }, ICE_RESTART_DELAY_MS * attempt)
    }
  }

  private closePeer(key: string): void {
    const peer = this.peers.get(key)
    if (!peer) return
    peer.connection.ontrack = null
    peer.connection.onicecandidate = null
    peer.connection.onnegotiationneeded = null
    peer.connection.onconnectionstatechange = null
    peer.connection.close()
    this.peers.delete(key)
    this.options.onRemoteStream(key, null)
  }

  /** Applies one relayed SDP/ICE payload from another participant. */
  async handleSignal(signal: InboundSignal): Promise<void> {
    if (this.closed) return
    const key = peerKey(signal.fromUserId, signal.fromDeviceId)
    let peer = this.peers.get(key)

    // The offerer may reach us before the roster update does; trust the relay,
    // since the server already checked both ends are in the call.
    if (!peer) {
      this.openPeer(signal.fromUserId, signal.fromDeviceId, key)
      peer = this.peers.get(key)
      if (!peer) return
    }

    const { connection } = peer

    try {
      if (signal.signalType === 'ice') {
        const candidate = JSON.parse(signal.payload) as RTCIceCandidateInit
        try {
          await connection.addIceCandidate(candidate)
        } catch (error) {
          // A candidate arriving while we are ignoring an offer has nowhere to
          // go; that is expected during glare, not a failure.
          if (!peer.ignoreOffer) throw error
        }
        return
      }

      const description = JSON.parse(signal.payload) as RTCSessionDescriptionInit

      if (description.type === 'offer') {
        const collision = peer.makingOffer || connection.signalingState !== 'stable'
        peer.ignoreOffer = !peer.polite && collision
        if (peer.ignoreOffer) return

        // setRemoteDescription implicitly rolls back a half-made local offer on
        // the polite side, which is what makes the collision recoverable.
        await connection.setRemoteDescription(description)
        await connection.setLocalDescription()
        if (!connection.localDescription) return
        this.options.sendSignal({
          toUserId: peer.userId,
          toDeviceId: peer.deviceId,
          signalType: 'answer',
          payload: JSON.stringify(connection.localDescription),
        })
        return
      }

      if (description.type === 'answer') {
        await connection.setRemoteDescription(description)
      }
    } catch (error) {
      this.options.onError(error instanceof Error ? error.message : 'signalling failed')
    }
  }

  close(): void {
    this.closed = true
    for (const key of [...this.peers.keys()]) this.closePeer(key)
  }
}
