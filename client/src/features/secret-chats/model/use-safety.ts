'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo } from 'react'

import { useSecretStore, useTrustStore, type PinnedIdentity } from '@/entities/secret-chat'
import { useSessionStore } from '@/entities/session'
import { MsgType, queryKeys, useSyncAppClient, type Wire } from '@/shared/api'
import { fromBase64 } from '@/shared/lib/e2e/codec'
import { safetyNumber } from '@/shared/lib/e2e/safety'

/**
 * The safety numbers of a peer's devices, and the controls for a key that
 * changed.
 *
 * One number per DEVICE, not per person: the identity being verified belongs to
 * a device, and collapsing several into one figure would mean a reinstall on one
 * phone invalidates the verification of another.
 */
export interface DeviceSafety {
  deviceId: string
  /** the peer user this device belongs to */
  userId: string
  /** the 60-digit number both sides should see identically */
  number: string
  /** what pinning thinks of this device's current keys */
  status: 'pinned' | 'unpinned' | 'changed'
  /** the recorded identity, when one exists */
  pinned?: PinnedIdentity
  /**
   * The exact key material this number was computed from.
   *
   * Carried so accepting pins what was DISPLAYED. Re-fetching at accept time
   * could pin a different key than the one whose number a human just compared,
   * which would quietly defeat the comparison.
   */
  identityKey: string
  signingKey: string
}

export function useSafetyNumbers(peerUserId: string, enabled: boolean) {
  const client = useSyncAppClient()
  const queryClient = useQueryClient()
  const selfId = useSessionStore((state) => state.session?.userId ?? '')
  const identity = useSecretStore((state) => state.identity)
  const pins = useTrustStore((state) => state.pins)

  const self = useMemo(() => {
    if (!identity || !selfId) return null
    return {
      stableId: selfId,
      identityKey: identity.identity.publicKey,
      signingKey: identity.signing.publicKey,
    }
  }, [identity, selfId])

  const load = useCallback(async (): Promise<DeviceSafety[]> => {
    if (!self) return []
    const reply = await client.request<Wire.KeyBundles>(
      MsgType.KEY_FETCH_ALL,
      { userId: peerUserId, deviceId: '' },
      { expect: MsgType.KEY_BUNDLES },
    )

    const trust = useTrustStore.getState()
    return reply.body.bundles.map((bundle) => {
      const deviceUserId = bundle.userId || peerUserId
      const verdict = trust.verify(
        deviceUserId,
        bundle.deviceId,
        bundle.identityKey,
        bundle.signingKey,
      )
      return {
        deviceId: bundle.deviceId,
        userId: deviceUserId,
        number: safetyNumber(self, {
          stableId: deviceUserId,
          identityKey: fromBase64(bundle.identityKey),
          signingKey: fromBase64(bundle.signingKey),
        }),
        status:
          verdict.kind === 'changed'
            ? 'changed'
            : verdict.kind === 'known'
              ? 'pinned'
              : 'unpinned',
        pinned: verdict.kind === 'changed' ? verdict.pinned : undefined,
        identityKey: bundle.identityKey,
        signingKey: bundle.signingKey,
      } satisfies DeviceSafety
    })
  }, [client, peerUserId, self])

  /**
   * The numbers, fetched on demand.
   *
   * `staleTime: 0` on purpose: a safety number is only meaningful at the moment
   * someone is comparing it, and a cached one is worse than none — it would show
   * a match against keys that are no longer in use.
   */
  const query = useQuery({
    queryKey: queryKeys.safety(peerUserId),
    enabled: enabled && self !== null && peerUserId.length > 0,
    staleTime: 0,
    gcTime: 0,
    queryFn: load,
  })

  /**
   * Records the keys the human just looked at.
   *
   * A mutation rather than a bare call so the list re-derives afterwards: the
   * status labels come from the pin store, and a panel that still says "not
   * recorded" after recording is a panel nobody will trust.
   */
  const accept = useMutation({
    mutationFn: async (device: DeviceSafety) => {
      useTrustStore
        .getState()
        .accept(device.userId, device.deviceId, device.identityKey, device.signingKey)
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.safety(peerUserId) }),
  })

  return { query, accept, pins, ready: self !== null }
}
