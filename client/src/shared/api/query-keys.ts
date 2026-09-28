/**
 * Central TanStack Query key registry.
 *
 * Keys live in one place so invalidation stays honest: a feature that changes
 * contacts invalidates `queryKeys.contacts()` rather than re-typing a tuple that
 * may or may not match the one the query used.
 */
export const queryKeys = {
  history: (chatId: string) => ['history', chatId] as const,
  contacts: () => ['contacts'] as const,
  pins: (chatId: string) => ['pins', chatId] as const,
  drafts: () => ['drafts'] as const,
  search: (query: string) => ['search', query] as const,
  /** `target` is a user id, "@username", or "" for our own profile. */
  profile: (target: string) => ['profile', target] as const,
  /** Every device signed in to this account. No argument: the account is the
   * connection, and a key that took one would imply a list you could ask for
   * about somebody else. */
  sessions: () => ['sessions'] as const,
  /** Safety numbers for one peer's devices, derived from their current bundles. */
  safety: (peerUserId: string) => ['safety', peerUserId] as const,
  /** The caller's own privacy settings. No argument: they are not readable for
   * anyone else, and a key that took one would imply they were. */
  privacy: () => ['privacy'] as const,
} as const
