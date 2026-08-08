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
} as const
