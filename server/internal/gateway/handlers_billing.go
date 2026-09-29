package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/SyncApp-chat/SyncApp/internal/billing"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

/*
The client side of billing.

Three things here are not obvious and are the reason this file exists rather than
the handlers being three lines each:

**The idempotency key is required, and is the client's.** A checkout that generates
its own key server-side is a checkout with no idempotency at all, because the key
differs on every retry — and the thing being retried is a charge. So a request
without one is refused rather than accommodated.

**Entitlements are sent explicitly, not derived from the plan name.** A client that
maps "premium" to a set of capabilities has a second copy of the policy, and the
two drift: raising the upload ceiling server-side would need every client updated
before anyone could use it.

**A subscription is PUSHED as well as replied to.** It changes without the client
asking — a payment settles, a period lapses — and a client still showing the old
tier offers features the server has started refusing, which reads as the app
breaking rather than as a plan ending.
*/

// sellsTiers reports whether this deployment has anything to sell.
//
// Not `svc.Billing == nil`: the billing service exists whenever a BillingStore
// does, and both stores implement one, while an ACQUIRER is the part that is
// genuinely optional. Asking about the service would put every account of a
// self-hosted instance with no acquirer on the FREE tier — locking secret chats
// behind a purchase the same deployment makes impossible (see
// `model.UngatedEntitlements`).
func (c *conn) sellsTiers() bool {
	return c.gw.svc.Billing != nil && c.gw.svc.Billing.SellsTiers()
}

// handleBillingPlans answers what the caller can buy.
func (c *conn) handleBillingPlans(ctx context.Context, e wire.Envelope) error {
	if !c.sellsTiers() {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "billing is not enabled")
	}
	var body wire.BillingPlansBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad plans body")
	}
	offers := c.gw.svc.Billing.Plans(normalizeCountry(body.Country))
	out := wire.BillingOffersBody{}
	for _, o := range offers {
		methods := make([]string, 0, len(o.Methods))
		for _, m := range o.Methods {
			methods = append(methods, string(m))
		}
		out.Offers = append(out.Offers, wire.PlanOfferWire{
			Plan: string(o.Plan), AmountMinor: o.AmountMinor, Currency: o.Currency,
			PeriodDays: o.PeriodDays, Methods: methods,
		})
	}
	return c.reply(wire.MsgBillingOffers, e.RequestID, out)
}

// handleBillingCheckout starts a payment.
func (c *conn) handleBillingCheckout(ctx context.Context, e wire.Envelope) error {
	if !c.sellsTiers() {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "billing is not enabled")
	}
	// Metered per USER, not per connection. A checkout is cheap to ask for and
	// reaches an external acquirer, so an unmetered loop is a way to make this
	// server hammer somebody else's API — and opening a second socket must not buy
	// a second budget.
	if !c.allowUser(ctx, "checkout") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many checkouts", 5000)
	}
	var body wire.BillingCheckoutBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad checkout body")
	}
	if strings.TrimSpace(body.IdempotencyKey) == "" {
		return c.replyError(e.RequestID, wire.ErrBadArg,
			"idempotency_key is required: without it a retry starts a second charge")
	}
	if len(body.IdempotencyKey) > maxIdempotencyKeyLen {
		return c.replyError(e.RequestID, wire.ErrBadArg, "idempotency_key is too long")
	}

	out, err := c.gw.svc.Billing.Checkout(ctx, billing.CheckoutRequest{
		UserID:         c.userID,
		Country:        normalizeCountry(body.Country),
		Method:         model.PaymentMethod(strings.ToLower(strings.TrimSpace(body.Method))),
		IdempotencyKey: body.IdempotencyKey,
		ReturnURL:      body.ReturnURL,
	})
	switch {
	case errors.Is(err, billing.ErrNoIdempotencyKey):
		return c.replyError(e.RequestID, wire.ErrBadArg, "idempotency_key is required")
	case errors.Is(err, billing.ErrBadMethod), errors.Is(err, billing.ErrNoPrice):
		return c.replyError(e.RequestID, wire.ErrBadArg, err.Error())
	case errors.Is(err, billing.ErrNoProvider):
		return c.replyError(e.RequestID, wire.ErrUnsupported, "no payment provider for your region")
	case errors.Is(err, billing.ErrChargeFailed), errors.Is(err, billing.ErrNoProviderRef):
		// The acquirer refused or misbehaved. Reported as a retryable error rather
		// than a bad request: the client did nothing wrong, and the operator needs to
		// see this in the logs, which replyForError would not guarantee.
		c.log.Warn("checkout failed", "user", logUser(c.userID), "err", err)
		return c.replyErrorRetry(e.RequestID, wire.ErrInternal, "the payment provider is unavailable", 5000)
	case err != nil:
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "billing.checkout", c.userID, out.Payment.ID, string(out.Payment.Method))
	return c.reply(wire.MsgBillingPayment, e.RequestID, wire.BillingPaymentBody{
		PaymentID:    out.Payment.ID,
		Status:       string(out.Payment.Status),
		AmountMinor:  out.Payment.AmountMinor,
		Currency:     out.Payment.Currency,
		PayURL:       out.PayURL,
		QRPayload:    out.QRPayload,
		Deduplicated: out.Deduplicated,
	})
}

// handleBillingStatus answers the caller's tier and entitlements.
func (c *conn) handleBillingStatus(ctx context.Context, e wire.Envelope) error {
	if !c.sellsTiers() {
		// No acquirer means there are no TIERS, so everything is granted and
		// nothing is for sale. A real answer rather than an error: the client needs its
		// ceilings either way, and it must not draw an upgrade prompt for a plan this
		// deployment does not offer.
		return c.reply(wire.MsgSubscription, e.RequestID,
			subscriptionToWire(nil, model.UngatedEntitlements()))
	}
	sub := c.gw.svc.Billing.Subscription(ctx, c.userID)
	ent := c.gw.svc.Billing.Entitlements(ctx, c.userID)
	return c.reply(wire.MsgSubscription, e.RequestID, subscriptionToWire(sub, ent))
}

// handleBillingCancel stops renewal without withdrawing access.
func (c *conn) handleBillingCancel(ctx context.Context, e wire.Envelope) error {
	if !c.sellsTiers() {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "billing is not enabled")
	}
	sub, err := c.gw.svc.Billing.Cancel(ctx, c.userID)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "billing.cancel", c.userID, "", "at period end")
	// Access lasts until the period ends, so the entitlements sent back are still
	// the paid ones. A client that hid Premium here would take away what the user
	// just paid for, which is the behaviour that teaches people not to cancel.
	return c.reply(wire.MsgSubscription, e.RequestID,
		subscriptionToWire(sub, c.gw.svc.Billing.Entitlements(ctx, c.userID)))
}

// subscriptionToWire renders a subscription and its entitlements.
//
// A nil subscription is the free tier rather than an error: every caller wants the
// TIER, and "no row" is a tier.
func subscriptionToWire(sub *model.Subscription, ent model.Entitlements) wire.SubscriptionBody {
	out := wire.SubscriptionBody{
		Plan:               string(ent.Plan),
		Status:             string(model.SubCanceled),
		SecretChats:        ent.SecretChats,
		MaxUploadBytes:     ent.MaxUploadBytes,
		MaxPinnedChats:     ent.MaxPinnedChats,
		Folders:            ent.Folders,
		AdvancedSearch:     ent.AdvancedSearch,
		PriorityDelivery:   ent.PriorityDelivery,
		VoiceTranscription: ent.VoiceTranscription,
		Badge:              ent.Badge,
		CustomThemes:       ent.CustomThemes,
	}
	if sub != nil {
		out.Status = string(sub.Status)
		out.PeriodEnd = sub.CurrentPeriodEnd
		out.CancelAtPeriodEnd = sub.CancelAtPeriodEnd
	}
	return out
}

// entitlements resolves what this connection's account may do.
//
// With NO billing configured, everything is granted — not the free tier.
//
// That distinction is the whole point and it is easy to get backwards. The free
// tier is what a non-paying account gets on a deployment that SELLS a paid one; it
// only means anything where there is something to buy. A self-hosted instance with
// no acquirer credentials has no tiers at all, so gating a feature there would
// withhold it from everybody with no way for anyone to obtain it — a paywall with
// nothing behind it.
//
// Getting this wrong is not subtle in effect: it disables secret chats on every
// deployment that does not take payments, which is most of them.
func (c *conn) entitlements(ctx context.Context) model.Entitlements {
	if !c.sellsTiers() {
		return model.UngatedEntitlements()
	}
	return c.gw.svc.Billing.Entitlements(ctx, c.userID)
}

// requirePremium replies with an upgrade prompt and reports whether it did.
//
// ErrPremiumRequired rather than ErrForbidden, because the two mean different
// things to a client: forbidden is final, while this one has an answer — show the
// upgrade screen. Conflating them makes a purchasable feature look broken.
func (c *conn) requirePremium(reqID uint64, has bool, feature string) bool {
	if has {
		return false
	}
	_ = c.replyError(reqID, wire.ErrPremiumRequired, feature+" requires Premium")
	return true
}

// normalizeCountry bounds and upper-cases a country code.
//
// The value comes from the client and selects an acquirer and a currency, so it is
// normalised rather than trusted: anything unrecognised falls through to the
// default market, which is a worse price for the user but never a broken checkout.
func normalizeCountry(country string) string {
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 {
		return ""
	}
	for i := 0; i < len(country); i++ {
		if country[i] < 'A' || country[i] > 'Z' {
			return ""
		}
	}
	return country
}
