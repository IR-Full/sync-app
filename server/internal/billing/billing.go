// Package billing owns subscriptions and payments.
//
// Almost none of the work in a payment integration is taking money. It is in
// surviving what happens afterwards: an acquirer sends its callback more than
// once, out of order, sometimes minutes late, sometimes for a payment this server
// has already written off — and every one of those has to produce the same final
// state as a single well-behaved notification would.
//
// So the two things this package is careful about are IDEMPOTENCY and a payment
// STATE MACHINE, and both live as close to the database as they can get (see
// store.BillingStore). What is here is the policy: which provider serves which
// country, what a plan costs, and what happens to a subscription when a payment
// settles.
package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/metrics"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

// New builds the billing service.
func New(st store.BillingStore, bus eventbus.Bus, ids *id.Generator, log *slog.Logger) *Service {
	return &Service{
		store:     st,
		providers: map[string]Provider{},
		bus:       bus,
		ids:       ids,
		log:       log,
		prices:    defaultPrices(),
	}
}

// WithProvider registers an acquirer. Later registrations of the same name win, so
// a deployment can override a default.
func (s *Service) WithProvider(p Provider) *Service {
	s.providers[p.Name()] = p
	return s
}

// WithPrice overrides the catalogue for one (plan, currency).
func (s *Service) WithPrice(plan model.Plan, currency string, amountMinor int64) *Service {
	s.prices[priceKey{plan, strings.ToUpper(currency)}] = amountMinor
	return s
}

// Plans lists what can be bought, in the currency for a country.
//
// Country-driven rather than a single price list, because the answer differs in
// three ways at once: the currency, the amount, and which payment methods exist. A
// catalogue that returned one of those and left the client to infer the others
// would show a Russian user a dollar price and a card form with no SBP.
func (s *Service) Plans(country string) []PlanOffer {
	currency := currencyFor(country)
	offers := make([]PlanOffer, 0, 1)
	amount, ok := s.prices[priceKey{model.PlanPremium, currency}]
	if !ok {
		return offers
	}
	offers = append(offers, PlanOffer{
		Plan:        model.PlanPremium,
		AmountMinor: amount,
		Currency:    currency,
		PeriodDays:  int32(subscriptionPeriod.Hours() / 24),
		Methods:     s.methodsFor(country),
	})
	return offers
}

// methodsFor lists the payment methods available in a country, in the order a
// client should offer them.
//
// Order matters and is not alphabetical: the first entry is what most people in
// that market expect to use, and a checkout that leads with the wrong one loses
// conversions to confusion rather than to price.
func (s *Service) methodsFor(country string) []model.PaymentMethod {
	provider := s.providerFor(country)
	if provider == nil {
		return nil
	}
	return provider.Methods()
}

// providerFor picks the acquirer for a country.
//
// Explicitly two markets rather than a general routing table, because that is what
// exists: a Russian acquirer that can do SBP, and one for everywhere else. A table
// would be honest about a generality the code does not have.
func (s *Service) providerFor(country string) Provider {
	name := ProviderStripe
	if isRussia(country) {
		name = ProviderYooKassa
	}
	if p, ok := s.providers[name]; ok {
		return p
	}
	// Fall back to anything configured. A deployment with one provider should work
	// everywhere it works, rather than refusing a country it could have served.
	for _, p := range s.providers {
		return p
	}
	return nil
}

// Checkout starts a payment and returns where to send the user.
//
// The order of operations is the whole design, and it is deliberately not the
// obvious one. The payment row is written BEFORE the provider is called, with no
// provider reference yet, because a callback can arrive before the charge response
// does — acquirers are fast and HTTP responses are not guaranteed. A webhook that
// finds no row has to either drop the money or invent an account for it, and both
// are worse than a row that is briefly missing its reference.
func (s *Service) Checkout(ctx context.Context, req CheckoutRequest) (*Checkout, error) {
	if req.UserID == "" {
		return nil, ErrNoUser
	}
	if req.IdempotencyKey == "" {
		// Required, not generated. A key the server invents is a key that differs on
		// every retry, which is the same as having none — and the caller is the only
		// party that knows two requests are the same request.
		return nil, ErrNoIdempotencyKey
	}
	currency := currencyFor(req.Country)
	amount, ok := s.prices[priceKey{model.PlanPremium, currency}]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoPrice, currency)
	}
	provider := s.providerFor(req.Country)
	if provider == nil {
		return nil, ErrNoProvider
	}
	method := req.Method
	if method == "" {
		method = provider.Methods()[0]
	}
	if !supports(provider, method) {
		return nil, fmt.Errorf("%w: %s does not offer %s", ErrBadMethod, provider.Name(), method)
	}

	now := nowMs()
	payment := &model.Payment{
		ID:             s.ids.NextString(),
		UserID:         req.UserID,
		Plan:           model.PlanPremium,
		Provider:       provider.Name(),
		Method:         method,
		AmountMinor:    amount,
		Currency:       currency,
		Status:         model.PayPending,
		IdempotencyKey: req.IdempotencyKey,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	stored, dup, err := s.store.CreatePayment(ctx, payment)
	if err != nil {
		return nil, err
	}
	if dup {
		// A retry. Returning the existing payment rather than charging again is the
		// entire point of the idempotency key — and the client gets the same answer
		// it would have got the first time, so a lost response costs nothing.
		metrics.CheckoutDeduped.Inc()
		return &Checkout{Payment: stored, Deduplicated: true}, nil
	}

	res, err := provider.Charge(ctx, ChargeRequest{
		UserID: req.UserID, Plan: model.PlanPremium, Method: method,
		AmountMinor: amount, Currency: currency,
		IdempotencyKey: req.IdempotencyKey, ReturnURL: req.ReturnURL,
	})
	if err != nil {
		// The row stays, as `pending`. Deleting it would break the idempotency key
		// (a retry would start a second charge at the acquirer, which may well have
		// accepted the first one despite the error we saw), and a pending payment
		// that never settles is collected by the expiry sweep.
		s.log.Warn("charge failed", "provider", provider.Name(), "payment", stored.ID, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrChargeFailed, err)
	}
	if res.ProviderRef == "" {
		// A provider with no stable reference cannot be made idempotent: there is
		// nothing for its callback to name. Refused rather than accepted, because the
		// failure would otherwise appear later as duplicate subscriptions.
		return nil, ErrNoProviderRef
	}
	if err := s.store.AttachProviderRef(ctx, stored.ID, res.ProviderRef); err != nil {
		return nil, err
	}
	stored.ProviderRef = res.ProviderRef

	// A subscription row in `incomplete` exists from here on, which is what makes
	// the payment callback an UPDATE rather than a create — and therefore safe to
	// receive twice.
	if err := s.ensureIncompleteSubscription(ctx, req.UserID, provider.Name(), now); err != nil {
		return nil, err
	}
	metrics.CheckoutStarted.WithLabelValues(provider.Name(), string(method)).Inc()
	return &Checkout{
		Payment: stored, PayURL: res.PayURL, QRPayload: res.QRPayload,
	}, nil
}

// ensureIncompleteSubscription creates the row a callback will update, without
// disturbing one that is already active.
func (s *Service) ensureIncompleteSubscription(ctx context.Context, userID, provider string, now int64) error {
	existing, err := s.store.GetSubscription(ctx, userID)
	switch {
	case err == nil && existing.Active(now):
		// Already paid. A second checkout while active is a renewal or an upgrade;
		// either way the live subscription must not be pushed back to `incomplete`,
		// which would take away access somebody is paying for.
		return nil
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	}
	return s.store.PutSubscription(ctx, &model.Subscription{
		UserID: userID, Plan: model.PlanPremium, Status: model.SubIncomplete,
		Provider: provider, CreatedAt: now, UpdatedAt: now,
	})
}

// HandleCallback authenticates and applies a provider notification.
//
// Everything hard about webhooks is in here, and the shape reflects it:
//
//  1. The body is VERIFIED first. A webhook endpoint is unauthenticated by
//     construction, so anything arriving there is an assertion by a stranger until
//     a signature says otherwise — and "it came from the right IP" is not a
//     signature.
//  2. The transition is applied by the STORE, inside a transaction, against the
//     stored status. A duplicate notification therefore changes nothing, and a
//     reordered one cannot move a payment backwards.
//  3. A callback that changes nothing is a SUCCESS. Reporting it as an error makes
//     the provider retry forever, which is how a retry storm starts.
func (s *Service) HandleCallback(ctx context.Context, providerName string, raw []byte, headers map[string]string) error {
	provider, ok := s.providers[providerName]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoProvider, providerName)
	}
	cb, err := provider.Verify(ctx, raw, headers)
	if err != nil {
		// Logged as a warning and returned: an unverifiable callback is either a
		// misconfiguration or someone probing, and both are worth seeing.
		metrics.WebhookRejected.WithLabelValues(providerName).Inc()
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	payment, err := s.store.GetPaymentByRef(ctx, providerName, cb.ProviderRef)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// A notification for a payment this server never started. Not an error to
			// retry: it means the reference is not ours, and telling the provider to
			// try again will not make it ours.
			s.log.Warn("callback for an unknown payment", "provider", providerName, "ref", cb.ProviderRef)
			metrics.WebhookUnknown.WithLabelValues(providerName).Inc()
			return nil
		}
		return err
	}
	// The amount is checked, because a callback that says a different number than
	// the payment was created for is either a provider bug or a forged body that
	// happened to verify — and granting a year of Premium for one kopek is the
	// failure mode worth one comparison.
	if cb.AmountMinor != 0 && cb.AmountMinor != payment.AmountMinor {
		s.log.Error("callback amount does not match the payment",
			"provider", providerName, "ref", cb.ProviderRef,
			"want", payment.AmountMinor, "got", cb.AmountMinor)
		return ErrAmountMismatch
	}

	at := cb.PaidAt
	if at == 0 {
		at = nowMs()
	}
	var sub *model.Subscription
	if cb.Status == model.PaySucceeded {
		sub = s.subscriptionAfterPayment(ctx, payment, at)
	}
	changed, err := s.store.ApplyPaymentStatus(ctx, providerName, cb.ProviderRef, cb.Status, at, sub)
	if err != nil {
		return err
	}
	if !changed {
		// The expected outcome for a retry. Success, so the provider stops.
		metrics.WebhookDuplicate.WithLabelValues(providerName).Inc()
		return nil
	}
	metrics.WebhookApplied.WithLabelValues(providerName, string(cb.Status)).Inc()
	if sub != nil {
		s.announce(ctx, sub)
	}
	return nil
}

// subscriptionAfterPayment computes the subscription a settled payment produces.
//
// The period EXTENDS an existing one rather than restarting it. Someone who renews
// early would otherwise lose the days they had already paid for, which is a refund
// request rather than a renewal.
func (s *Service) subscriptionAfterPayment(ctx context.Context, p *model.Payment, at int64) *model.Subscription {
	sub, err := s.store.GetSubscription(ctx, p.UserID)
	if err != nil {
		sub = &model.Subscription{UserID: p.UserID, CreatedAt: at}
	}
	from := at
	if sub.CurrentPeriodEnd > at {
		from = sub.CurrentPeriodEnd
	}
	sub.Plan = p.Plan
	sub.Status = model.SubActive
	sub.Provider = p.Provider
	sub.CurrentPeriodEnd = from + subscriptionPeriod.Milliseconds()
	sub.CancelAtPeriodEnd = false
	sub.UpdatedAt = at
	if sub.CreatedAt == 0 {
		sub.CreatedAt = at
	}
	return sub
}

// Cancel stops the subscription renewing, WITHOUT withdrawing access.
//
// The period is paid for. A product that takes away what was bought the moment
// someone clicks cancel teaches them not to click it — so they keep the plan until
// it lapses and the expiry sweep closes it.
func (s *Service) Cancel(ctx context.Context, userID string) (*model.Subscription, error) {
	sub, err := s.store.GetSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}
	sub.CancelAtPeriodEnd = true
	sub.UpdatedAt = nowMs()
	if err := s.store.PutSubscription(ctx, sub); err != nil {
		return nil, err
	}
	s.announce(ctx, sub)
	return sub, nil
}

// Subscription returns an account's subscription, or a free-tier placeholder.
//
// A placeholder rather than ErrNotFound, because every caller wants to know the
// TIER and "no row" is a tier. Making them each translate the error is how one of
// them forgets and treats the absence as an outage.
func (s *Service) Subscription(ctx context.Context, userID string) *model.Subscription {
	sub, err := s.store.GetSubscription(ctx, userID)
	if err != nil {
		return &model.Subscription{UserID: userID, Plan: model.PlanFree, Status: model.SubCanceled}
	}
	return sub
}

// Entitlements resolves what an account may do right now.
func (s *Service) Entitlements(ctx context.Context, userID string) model.Entitlements {
	return model.EntitlementsFor(s.Subscription(ctx, userID), nowMs())
}

// Payments returns an account's receipts.
func (s *Service) Payments(ctx context.Context, userID string, limit int) ([]*model.Payment, error) {
	return s.store.ListPayments(ctx, userID, limit)
}

// RunExpiry closes lapsed subscriptions until ctx ends.
//
// It announces every account it touches, because entitlements change the moment a
// period ends: a client still showing Premium will offer features the server now
// refuses, which the user experiences as the app breaking rather than as a
// subscription expiring.
func (s *Service) RunExpiry(ctx context.Context) {
	t := time.NewTicker(expirySweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepExpired(ctx)
		}
	}
}

func (s *Service) sweepExpired(ctx context.Context) {
	users, err := s.store.ExpireSubscriptions(ctx, nowMs(), expirySweepBatch)
	if err != nil {
		s.log.Warn("subscription expiry sweep failed", "err", err)
		return
	}
	for _, uid := range users {
		metrics.SubscriptionExpired.Inc()
		s.announce(ctx, s.Subscription(ctx, uid))
	}
}

// announce publishes a subscription change so every connected device of the
// account learns its new entitlements without polling.
func (s *Service) announce(ctx context.Context, sub *model.Subscription) {
	if s.bus == nil || sub == nil {
		return
	}
	ent := model.EntitlementsFor(sub, nowMs())
	if err := s.bus.Publish(ctx, eventbus.Event{
		Subject: eventbus.SubjSubscription,
		Key:     sub.UserID,
		Data:    encodeSubscriptionEvent(sub, ent),
	}); err != nil {
		s.log.Warn("subscription announce failed", "user", sub.UserID, "err", err)
	}
}

func supports(p Provider, m model.PaymentMethod) bool {
	for _, have := range p.Methods() {
		if have == m {
			return true
		}
	}
	return false
}

func nowMs() int64 { return time.Now().UnixMilli() }
