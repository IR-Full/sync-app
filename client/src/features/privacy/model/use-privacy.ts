'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { MsgType, queryKeys, useIsConnected, useSyncAppClient, type Wire } from '@/shared/api'

/**
 * Who may see what.
 *
 * Three settings rather than one level, mirroring the server: they are enforced
 * on different paths and answer different questions — presence fanout asks about
 * last-seen, a profile read asks about the avatar, and a group add asks whether
 * the caller may add this account at all.
 */
export type Visibility = 'everyone' | 'contacts' | 'nobody'

export const VISIBILITIES: Visibility[] = ['everyone', 'contacts', 'nobody']

export interface PrivacySettings {
  lastSeen: Visibility
  avatar: Visibility
  groups: Visibility
}

/**
 * Anything the server sends that this build does not recognise reads as the
 * strictest setting rather than the loosest.
 *
 * The asymmetry is deliberate. If a newer server grows a narrower option, an
 * older client guessing "everyone" would draw a settings screen claiming the
 * account is public when the server is keeping it private — and a user who then
 * saves that screen has quietly widened their own visibility. Guessing "nobody"
 * is wrong in the direction that cannot leak anything.
 */
function toVisibility(value: string): Visibility {
  return (VISIBILITIES as string[]).includes(value) ? (value as Visibility) : 'nobody'
}

export function usePrivacy() {
  const client = useSyncAppClient()
  const connected = useIsConnected()

  return useQuery({
    queryKey: queryKeys.privacy(),
    enabled: connected,
    staleTime: 60_000,
    queryFn: async (): Promise<PrivacySettings> => {
      const reply = await client.request<Wire.Privacy>(
        MsgType.PRIVACY_GET,
        {},
        {
          expect: MsgType.PRIVACY,
        },
      )
      return {
        lastSeen: toVisibility(reply.body.lastSeen),
        avatar: toVisibility(reply.body.avatar),
        groups: toVisibility(reply.body.groups),
      }
    },
  })
}

/**
 * Replaces the settings.
 *
 * Every field is sent every time, because the server requires it: a partial
 * update would make "nobody" indistinguishable from "not specified", and the
 * setting whose whole purpose is to withhold something is the worst one to have
 * an ambiguous empty value.
 */
export function useSetPrivacy() {
  const client = useSyncAppClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (next: PrivacySettings) => {
      const reply = await client.request<Wire.Privacy>(
        MsgType.PRIVACY_SET,
        { lastSeen: next.lastSeen, avatar: next.avatar, groups: next.groups },
        { expect: MsgType.PRIVACY },
      )
      return reply.body
    },
    // Re-read rather than trust the echo: the server is the authority on what it
    // stored, and a rejected field must not leave the UI showing a value that
    // was never saved.
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.privacy() }),
  })
}
