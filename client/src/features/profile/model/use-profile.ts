'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { useSessionStore } from '@/entities/session'
import { MsgType, queryKeys, useIsConnected, useSyncAppClient, type Wire } from '@/shared/api'

export interface Profile {
  userId: string
  username: string
  displayName: string
  avatarRef: string
}

function toProfile(body: Wire.Profile): Profile {
  return {
    userId: body.userId,
    username: body.username,
    displayName: body.displayName,
    avatarRef: body.avatarRef,
  }
}

/**
 * A user's public profile.
 *
 * `target` is a user id or `"@username"` — the same message serves both, which
 * makes this the user lookup as well: there is no directory and no prefix
 * search, so an exact handle is all a client can go on. An empty target asks
 * for our own.
 *
 * The reply carries nothing private, so it is cached like any other read. It is
 * kept fresh for a minute rather than indefinitely: a name can change on
 * another device, and only our OWN change is pushed back to us.
 */
export function useProfile(target: string) {
  const client = useSyncAppClient()
  const connected = useIsConnected()

  return useQuery({
    queryKey: queryKeys.profile(target),
    enabled: connected,
    staleTime: 60_000,
    queryFn: async (): Promise<Profile> => {
      const reply = await client.request<Wire.Profile>(
        MsgType.PROFILE_GET,
        { target },
        { expect: MsgType.PROFILE },
      )
      return toProfile(reply.body)
    },
  })
}

export interface ProfileUpdate {
  displayName?: string
  /** A media_ref from the ordinary upload pipeline. */
  avatarRef?: string
  /** Removes the avatar. Explicit, because an empty ref means "leave it alone". */
  clearAvatar?: boolean
}

/**
 * Changes our own profile — the protocol has no way to address anyone else's.
 *
 * Omitted fields are left as they are: proto3 cannot distinguish an absent
 * string from an empty one, so the server reads an empty `avatar_ref` as "no
 * change" and takes `clear_avatar` for the removal. Sending an empty display
 * name is therefore a no-op, not a way to erase it.
 *
 * The gateway mirrors the change to this account's other devices as an
 * unsolicited PROFILE frame; the reply we get here is the same body, so the
 * local session is updated from one place either way.
 */
export function useUpdateProfile() {
  const client = useSyncAppClient()
  const queryClient = useQueryClient()
  const updateSession = useSessionStore((state) => state.updateSession)

  return useMutation({
    mutationFn: async (update: ProfileUpdate): Promise<Profile> => {
      const reply = await client.request<Wire.Profile>(
        MsgType.PROFILE_SET,
        {
          displayName: update.displayName ?? '',
          avatarRef: update.avatarRef ?? '',
          clearAvatar: update.clearAvatar ?? false,
        },
        { expect: MsgType.PROFILE },
      )
      return toProfile(reply.body)
    },
    onSuccess: (profile) => {
      updateSession({
        username: profile.username,
        displayName: profile.displayName,
        avatarRef: profile.avatarRef,
      })
      queryClient.setQueryData(queryKeys.profile(''), profile)
      queryClient.setQueryData(queryKeys.profile(profile.userId), profile)
      queryClient.setQueryData(queryKeys.profile(`@${profile.username}`), profile)
    },
  })
}
