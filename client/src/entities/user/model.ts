'use client'

import { create } from 'zustand'

export interface KnownUser {
  userId: string
  /** "@name" when we learned it from a handle we typed or a contact */
  username?: string
  /** contact name from the address book, if any */
  name?: string
  online?: boolean
  lastSeenMs?: number
}

interface UserDirectoryState {
  users: Record<string, KnownUser>
  upsert: (user: KnownUser) => void
  upsertMany: (users: KnownUser[]) => void
  setPresence: (userId: string, online: boolean, lastSeenMs: number) => void
  clear: () => void
}

/**
 * Everything this client knows about other users.
 *
 * The protocol has no "fetch user profile" message — a NEW frame carries a
 * `sender_id` and nothing else, and usernames only ever travel in the direction
 * client→server (as "@name" targets the gateway resolves). So names are
 * assembled from the sources that do carry them: the contact list, handles the
 * user typed, and presence frames.
 *
 * In-memory only: it is a cache of derived labels, cheap to rebuild on the next
 * contact sync, and stale names are worse than absent ones.
 */
export const useUserDirectory = create<UserDirectoryState>((set) => ({
  users: {},

  upsert: (user) =>
    set((state) => ({
      users: { ...state.users, [user.userId]: { ...state.users[user.userId], ...user } },
    })),

  upsertMany: (incoming) =>
    set((state) => {
      const users = { ...state.users }
      for (const user of incoming) {
        users[user.userId] = { ...users[user.userId], ...user }
      }
      return { users }
    }),

  setPresence: (userId, online, lastSeenMs) =>
    set((state) => ({
      users: {
        ...state.users,
        [userId]: { ...state.users[userId], userId, online, lastSeenMs },
      },
    })),

  clear: () => set({ users: {} }),
}))

/**
 * Best label available for a user id, in descending order of usefulness.
 * Falls back to a shortened id so a sender is never rendered as an empty string.
 */
export function labelForUser(user: KnownUser | undefined, userId: string): string {
  if (user?.name) return user.name
  if (user?.username) return `@${user.username.replace(/^@/, '')}`
  return `#${userId.slice(-6)}`
}

export function useUserLabel(userId: string): string {
  const user = useUserDirectory((state) => state.users[userId])
  return labelForUser(user, userId)
}
