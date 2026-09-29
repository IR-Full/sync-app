'use client'

import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { useSubscriptionStore } from '@/entities/subscription'
import { ErrorCode, ProtocolError } from '@/shared/api'
import { useLocale, useTranslate } from '@/shared/i18n'
import { Button } from '@/shared/ui'

import { formatPrice, useBilling, type Checkout, type PlanOffer } from '../model/use-billing'

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border-line bg-surface rounded-2xl border p-4">
      <h2 className="text-ink mb-2 text-sm font-semibold">{title}</h2>
      {children}
    </section>
  )
}

/**
 * The country the plans are priced for.
 *
 * Taken from the browser rather than asked, because it selects the PROVIDER as well as
 * the currency — SBP in Russia, cards elsewhere — and letting the user pick would let
 * them pick which regulator applies to their transaction. The server treats it as a
 * hint and decides for itself.
 */
function guessCountry(locale: string): string {
  const region = new Intl.Locale(locale).maximize().region
  return region ?? ''
}

/** Where the user finishes paying. */
function PaymentStep({ checkout }: { checkout: Checkout }) {
  const t = useTranslate()
  return (
    <div className="border-line bg-surface-sunken mt-3 flex flex-col gap-3 rounded-xl border p-3">
      {/*
        The two shapes are NOT interchangeable, and the difference is most of this
        component. `payUrl` is followed in a browser; an SBP `qrPayload` is displayed
        for a bank app to scan. Rendering a payload as a link produces a dead link, and
        rendering a URL as a payload produces a code that goes to a web page instead of
        to a payment.
      */}
      {checkout.qrPayload && (
        <div>
          <p className="text-ink text-xs font-semibold">{t('premium.qr')}</p>
          <p className="text-ink mt-1 font-mono text-xs break-all select-all">
            {checkout.qrPayload}
          </p>
        </div>
      )}
      {checkout.payUrl && (
        <a
          href={checkout.payUrl}
          target="_blank"
          rel="noreferrer noopener"
          className="bg-accent text-on-accent inline-flex w-fit items-center rounded-xl px-3 py-1.5 text-xs font-medium"
        >
          {t('premium.pay')}
        </a>
      )}
    </div>
  )
}

/**
 * The Premium screen: what the tier grants, what it costs, and how to pay.
 *
 * Entitlements are read from the store rather than from the plan name. That matters
 * beyond tidiness: a deployment with no billing service configured reports the plan as
 * `free` while granting everything, so a panel that gated on `plan === 'premium'` would
 * hide the features on exactly the installation where they are all available.
 */
export function PremiumPanel() {
  const t = useTranslate()
  const locale = useLocale()
  const billing = useBilling()
  const entitlements = useSubscriptionStore((state) => state.entitlements)

  const [checkout, setCheckout] = useState<Checkout | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const country = guessCountry(locale)

  /*
   * The plans, through react-query rather than an effect that sets state.
   *
   * Not only to satisfy the lint rule: a fetch-then-setState effect re-runs on every
   * identity change of its dependencies, and the alternative — fetching once and never
   * again — leaves a price on screen that a server-side change cannot correct. A query
   * keyed on the country gets refetching, deduplication across mounts and a real
   * loading state for free.
   *
   * A gateway with no payment provider answers UNSUPPORTED. That is mapped to an EMPTY
   * list rather than to an error, because it is not one: such a deployment grants the
   * features outright, and the panel should say payments are not configured.
   */
  const plans = useQuery<PlanOffer[]>({
    queryKey: ['billing', 'plans', country],
    queryFn: async () => {
      try {
        return await billing.loadPlans(country)
      } catch (failure) {
        if (failure instanceof ProtocolError && failure.code === ErrorCode.UNSUPPORTED) {
          return []
        }
        throw failure
      }
    },
  })

  const offers = plans.data ?? null
  const isPremium = entitlements.plan === 'premium'

  async function start(plan: string, method: string) {
    setError('')
    setBusy(true)
    try {
      setCheckout(await billing.checkout({ country, method }))
    } catch (failure) {
      setError(
        failure instanceof ProtocolError && failure.message
          ? failure.message
          : t('error.unknown'),
      )
    } finally {
      setBusy(false)
    }
  }

  const periodEnd =
    entitlements.periodEnd > 0
      ? new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(entitlements.periodEnd)
      : null

  return (
    <Section title={t('premium.title')}>
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-ink-muted text-xs">{t('premium.plan')}</span>
        <span
          className={isPremium ? 'text-accent text-sm font-semibold' : 'text-ink-muted text-sm'}
        >
          {isPremium ? t('premium.plan.premium') : t('premium.plan.free')}
        </span>
      </div>
      {periodEnd && (
        <p className="text-ink-muted mt-1 text-xs">{t('premium.until', { date: periodEnd })}</p>
      )}
      {entitlements.cancelAtPeriodEnd && (
        <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
          {t('premium.cancelling')}
        </p>
      )}
      <p className="text-ink-muted mt-2 text-xs">{t('premium.subtitle')}</p>

      {plans.isError ? (
        <p className="text-danger mt-3 text-xs">{t('error.unknown')}</p>
      ) : offers === null ? (
        <p className="text-ink-faint mt-3 text-xs">{t('common.loading')}</p>
      ) : offers.length === 0 ? (
        <p className="text-ink-muted mt-3 text-xs">{t('premium.unavailable')}</p>
      ) : (
        <div className="mt-3 flex flex-col gap-3">
          {offers.map((offer) => (
            <div key={offer.plan} className="border-line rounded-xl border p-3">
              <div className="flex items-baseline justify-between gap-3">
                <span className="text-ink text-sm font-semibold">
                  {formatPrice(offer.amountMinor, offer.currency, locale)}
                </span>
                <span className="text-ink-faint text-xs">
                  {t('premium.period', { days: offer.periodDays })}
                </span>
              </div>
              <div className="mt-2 flex flex-wrap gap-2">
                {/*
                  One button per method the SERVER offered for this plan and country —
                  not the full set of methods this client knows about. A button for a
                  method that is unavailable here fails only after the user has
                  committed to paying.
                */}
                {offer.methods.map((method) => (
                  <Button
                    key={method}
                    size="small"
                    variant="secondary"
                    disabled={busy}
                    onClick={() => void start(offer.plan, method)}
                  >
                    {method === 'sbp' ? t('premium.method.sbp') : t('premium.method.card')}
                  </Button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {checkout && <PaymentStep checkout={checkout} />}
      {error && <p className="text-danger mt-2 text-xs">{error}</p>}

      {isPremium && !entitlements.cancelAtPeriodEnd && (
        <div className="border-line mt-3 border-t pt-3">
          <Button
            size="small"
            variant="danger"
            disabled={busy}
            onClick={() => {
              setBusy(true)
              // Cancelling does NOT withdraw access — the paid period runs out first —
              // so nothing here needs to hide features. The store is updated from the
              // reply, which still carries the paid entitlements.
              void billing
                .cancel()
                .catch(() => setError(t('error.unknown')))
                .finally(() => setBusy(false))
            }}
          >
            {t('premium.cancel')}
          </Button>
        </div>
      )}
    </Section>
  )
}
