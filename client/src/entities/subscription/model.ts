'use client'

import { create } from 'zustand'

import type { Wire } from '@/shared/api'

/**
 * What this account may do, as the server sees it.
 *
 * Entitlements are stored as the server SENT them rather than derived from the plan
 * name, and that is the important decision here. A client that mapped
 * `plan === 'premium'` to a set of capabilities would hold a second copy of the
 * policy, and the two drift: a server that raises the upload ceiling would need every
 * client updated before anybody could use the new one.
 *
 * The store is also written from a PUSH, not only from a request. A subscription
 * changes without the client asking — a payment settles, a period lapses — and until
 * the client hears about it it goes on offering features the server has started
 * refusing, which reads as the app breaking rather than as a plan ending.
 */
export interface Entitlements {
  plan: 'free' | 'premium'
  status: string
  /** when access lapses without a renewal, unix millis; 0 = no expiry */
  periodEnd: number
  cancelAtPeriodEnd: boolean

  secretChats: boolean
  maxUploadBytes: number
  maxPinnedChats: number
  folders: boolean
  advancedSearch: boolean
  priorityDelivery: boolean
  voiceTranscription: boolean
  badge: boolean
}

/**
 * The state before the server has said anything.
 *
 * Everything OFF, which is the safe direction for a UI: a feature drawn as available
 * and then refused is worse than one that appears a moment late. The one thing that
 * must not happen is a client deciding for itself that it is Premium.
 */
export const UNKNOWN_ENTITLEMENTS: Entitlements = {
  plan: 'free',
  status: '',
  periodEnd: 0,
  cancelAtPeriodEnd: false,
  secretChats: false,
  maxUploadBytes: 0,
  maxPinnedChats: 0,
  folders: false,
  advancedSearch: false,
  priorityDelivery: false,
  voiceTranscription: false,
  badge: false,
}

interface SubscriptionState {
  /** false until the server has answered once */
  known: boolean
  entitlements: Entitlements
  apply: (body: Wire.Subscription) => void
  reset: () => void
}

export const useSubscriptionStore = create<SubscriptionState>((set) => ({
  known: false,
  entitlements: UNKNOWN_ENTITLEMENTS,

  apply: (body) =>
    set({
      known: true,
      entitlements: {
        plan: body.plan === 'premium' ? 'premium' : 'free',
        status: body.status ?? '',
        periodEnd: body.periodEnd ?? 0,
        cancelAtPeriodEnd: body.cancelAtPeriodEnd ?? false,
        secretChats: body.secretChats ?? false,
        maxUploadBytes: body.maxUploadBytes ?? 0,
        maxPinnedChats: body.maxPinnedChats ?? 0,
        folders: body.folders ?? false,
        advancedSearch: body.advancedSearch ?? false,
        priorityDelivery: body.priorityDelivery ?? false,
        voiceTranscription: body.voiceTranscription ?? false,
        badge: body.badge ?? false,
      },
    }),

  // Not persisted, and deliberately: entitlements are cheap to re-fetch and
  // expensive to be wrong about. A cached "premium" surviving a logout would offer
  // paid features to whoever logs in next.
  reset: () => set({ known: false, entitlements: UNKNOWN_ENTITLEMENTS }),
}))

/** Whether a named capability is available. */
export function useEntitlement<K extends keyof Entitlements>(key: K): Entitlements[K] {
  return useSubscriptionStore((state) => state.entitlements[key])
}

export function useEntitlements(): Entitlements {
  return useSubscriptionStore((state) => state.entitlements)
}

/**
 * Whether the account is on the paid plan right NOW.
 *
 * Checked against the period rather than the status alone: a cancelled subscription
 * keeps its access until the period it was paid for ends, so a client that hid
 * Premium the moment `cancelAtPeriodEnd` was set would take away what the user had
 * already bought.
 */
export function isPremiumActive(ent: Entitlements, now = Date.now()): boolean {
  if (ent.plan !== 'premium') return false
  return ent.periodEnd === 0 || ent.periodEnd > now
}
