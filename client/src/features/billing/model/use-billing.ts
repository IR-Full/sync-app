'use client'

import { useCallback, useEffect, useRef } from 'react'

import { useSessionStore } from '@/entities/session'
import { useSubscriptionStore } from '@/entities/subscription'
import { MsgType, useIsConnected, useSyncAppClient, type Wire } from '@/shared/api'
import { createDedupKey } from '@/shared/lib/id'

/**
 * Keeps this client's entitlements current.
 *
 * Two halves, and the second is the one that is easy to leave out. Asking on connect
 * is obvious; LISTENING is what stops the client from being wrong for hours. A
 * subscription changes without any request from here — a payment settles minutes after
 * the user left the checkout page, a period lapses at four in the morning — and until
 * the client hears about it it goes on drawing features the server now refuses. The
 * user experiences that as the app breaking, not as a plan ending.
 */
export function useSubscriptionSync(): void {
  const client = useSyncAppClient()
  const connected = useIsConnected()
  const userId = useSessionStore((state) => state.session?.userId ?? '')
  const askedFor = useRef<string | null>(null)

  // The PUSH. Subscribed for the whole session rather than alongside the request,
  // because the event can arrive at any moment and a handler mounted only during the
  // initial fetch would miss every later change.
  useEffect(() => {
    return client.on('subscription', (body) => {
      useSubscriptionStore.getState().apply(body)
    })
  }, [client])

  useEffect(() => {
    if (!connected || !userId) {
      askedFor.current = null
      return
    }
    if (askedFor.current === userId) return
    askedFor.current = userId

    let cancelled = false
    void (async () => {
      try {
        const reply = await client.request<Wire.Subscription>(
          MsgType.BILLING_STATUS,
          {},
          { expect: MsgType.SUBSCRIPTION },
        )
        if (!cancelled) useSubscriptionStore.getState().apply(reply.body)
      } catch {
        // An older gateway does not know this type. Left as UNKNOWN rather than
        // guessed: everything off is the safe direction, and a feature that appears a
        // moment late is better than one drawn as available and then refused.
      }
    })()
    return () => {
      cancelled = true
    }
  }, [client, connected, userId])
}

/** One purchasable plan in this market. */
export interface PlanOffer {
  plan: string
  amountMinor: number
  currency: string
  periodDays: number
  methods: string[]
}

/** Where to send the user to pay. */
export interface Checkout {
  paymentId: string
  status: string
  /**
   * `payUrl` is FOLLOWED; `qrPayload` is DISPLAYED as a QR code. They are not
   * interchangeable — an SBP payload is not a URL, and rendering it as a link
   * produces a broken one.
   */
  payUrl?: string
  qrPayload?: string
  /** true when this repeated an earlier request and no new charge was made */
  deduplicated: boolean
}

export function useBilling(): {
  loadPlans: (country: string) => Promise<PlanOffer[]>
  checkout: (opts: {
    country: string
    method?: string
    returnUrl?: string
  }) => Promise<Checkout>
  cancel: () => Promise<void>
} {
  const client = useSyncAppClient()

  const loadPlans = useCallback(
    async (country: string) => {
      const reply = await client.request<Wire.BillingOffers>(
        MsgType.BILLING_PLANS,
        { country },
        { expect: MsgType.BILLING_OFFERS },
      )
      return (reply.body.offers ?? []).map((o) => ({
        plan: o.plan,
        amountMinor: o.amountMinor,
        currency: o.currency,
        periodDays: o.periodDays,
        methods: o.methods ?? [],
      }))
    },
    [client],
  )

  const checkout = useCallback(
    async (opts: { country: string; method?: string; returnUrl?: string }) => {
      const reply = await client.request<Wire.BillingPayment>(
        MsgType.BILLING_CHECKOUT,
        {
          plan: 'premium',
          country: opts.country,
          ...(opts.method ? { method: opts.method } : {}),
          ...(opts.returnUrl ? { returnUrl: opts.returnUrl } : {}),
          /*
           * The idempotency key, and it is REQUIRED by the server rather than
           * optional.
           *
           * It is generated here, per call, and that is the correct scope: one user
           * action is one payment. If the request times out, the transport layer's
           * retry reuses this same body — so it reaches the same payment instead of
           * starting a second charge. A key the server invented would differ on every
           * attempt, which is the same as having none.
           */
          idempotencyKey: createDedupKey(),
        },
        { expect: MsgType.BILLING_PAYMENT },
      )
      return {
        paymentId: reply.body.paymentId,
        status: reply.body.status,
        ...(reply.body.payUrl ? { payUrl: reply.body.payUrl } : {}),
        ...(reply.body.qrPayload ? { qrPayload: reply.body.qrPayload } : {}),
        deduplicated: reply.body.deduplicated ?? false,
      }
    },
    [client],
  )

  const cancel = useCallback(async () => {
    const reply = await client.request<Wire.Subscription>(
      MsgType.BILLING_CANCEL,
      {},
      { expect: MsgType.SUBSCRIPTION },
    )
    // The reply still carries the PAID entitlements, because cancelling does not
    // withdraw access — the period is paid for. Applying it verbatim is what keeps the
    // UI from taking away what the user just bought the moment they cancel.
    useSubscriptionStore.getState().apply(reply.body)
  }, [client])

  return { loadPlans, checkout, cancel }
}

/**
 * Formats a price from INTEGER minor units.
 *
 * The amount is never a float anywhere in this system — not in the database, not on
 * the wire, and not here. `amountMinor / 100` would introduce the error this avoids:
 * 0.01 has no exact binary representation, so a price formatted through a float is
 * occasionally not the price that will be charged.
 */
export function formatPrice(amountMinor: number, currency: string, locale: string): string {
  const whole = Math.trunc(amountMinor / 100)
  const cents = Math.abs(amountMinor % 100)
  const text = `${whole}.${String(cents).padStart(2, '0')}`
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency }).format(Number(text))
  } catch {
    // An unknown currency code: show the number and the code rather than nothing.
    return `${text} ${currency}`
  }
}
