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
	"encoding/json"
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

// Provider names. They are written into the payments table and read back to route
// refunds, so they are stable identifiers rather than display strings.
const (
	ProviderYooKassa = "yookassa"
	ProviderStripe   = "stripe"
)

// subscriptionPeriod is how long one payment buys.
//
// 30 days rather than a calendar month, and the difference is worth being explicit
// about: calendar months are 28 to 31 days, so a monthly subscription billed on the
// 31st has to decide what February means. A fixed period has no such edge, and the
// renewal date drifting slowly through the month is a smaller surprise than a
// missed charge.
const subscriptionPeriod = 30 * 24 * time.Hour

// The expiry sweep. Bounded per pass so a large backlog is cleared in chunks
// rather than in one statement that holds locks for as long as it takes.
const (
	expirySweepEvery = 5 * time.Minute
	expirySweepBatch = 500
)

// defaultPrices is the catalogue.
//
// Prices live in code with an override hook rather than in the database, because a
// price change is a deliberate act that should go through review — and a catalogue
// an operator can edit at runtime is one where a typo charges everybody a hundred
// times too much. WithPrice exists for deployments that disagree.
func defaultPrices() map[priceKey]int64 {
	return map[priceKey]int64{
		// 299 RUB and 3.99 USD, in minor units. Integers throughout: a price is an
		// exact quantity and binary floating point cannot hold 0.01.
		{model.PlanPremium, "RUB"}: 29900,
		{model.PlanPremium, "USD"}: 399,
		{model.PlanPremium, "EUR"}: 399,
	}
}

// currencyFor maps a country to the currency it is billed in.
//
// Deliberately coarse. The alternative — a full country-to-currency table — would
// imply the service can settle in dozens of currencies, which it cannot: there are
// two acquirers, and everywhere outside Russia is billed in one of two currencies.
func currencyFor(country string) string {
	switch {
	case isRussia(country):
		return "RUB"
	case isEurozone(country):
		return "EUR"
	default:
		return "USD"
	}
}

func isRussia(country string) bool {
	return strings.EqualFold(strings.TrimSpace(country), "RU")
}

func isEurozone(country string) bool {
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "AT", "BE", "CY", "DE", "EE", "ES", "FI", "FR", "GR", "HR", "IE",
		"IT", "LT", "LU", "LV", "MT", "NL", "PT", "SI", "SK":
		return true
	default:
		return false
	}
}

// CheckoutRequest is what a client asks for.
type CheckoutRequest struct {
	UserID string
	// Country selects the acquirer, the currency and the available methods. It comes
	// from the client rather than from a GeoIP lookup on purpose: the user knows
	// which market they are in, and a lookup that guesses wrong offers a payment
	// method their bank does not support.
	Country string
	Method  model.PaymentMethod
	// IdempotencyKey is required. A key the server generates differs on every retry,
	// which is the same as having none.
	IdempotencyKey string
	ReturnURL      string
}

// Checkout is where to send the user.
type Checkout struct {
	Payment *model.Payment
	// PayURL is a redirect (card flows); QRPayload is an SBP QR string, which is
	// SHOWN rather than followed. The two are not interchangeable, and a client that
	// renders a QR payload as a link produces a broken one.
	PayURL    string
	QRPayload string
	// Deduplicated means this was a repeated request and no new charge was made.
	Deduplicated bool
}

// PlanOffer is one purchasable plan in one market.
type PlanOffer struct {
	Plan        model.Plan
	AmountMinor int64
	Currency    string
	PeriodDays  int32
	Methods     []model.PaymentMethod
}

// subscriptionEvent is what goes on the bus when a subscription changes.
//
// JSON and a named struct, for the reason documented in internal/fanout: the wire
// codec is protobuf-backed and has no mapping for an ad-hoc type, so Marshal would
// silently publish zero bytes.
type subscriptionEvent struct {
	UserID       string                   `json:"user_id"`
	Plan         model.Plan               `json:"plan"`
	Status       model.SubscriptionStatus `json:"status"`
	PeriodEnd    int64                    `json:"period_end"`
	Entitlements model.Entitlements       `json:"entitlements"`
}

func encodeSubscriptionEvent(sub *model.Subscription, ent model.Entitlements) []byte {
	b, _ := json.Marshal(subscriptionEvent{
		UserID: sub.UserID, Plan: sub.Plan, Status: sub.Status,
		PeriodEnd: sub.CurrentPeriodEnd, Entitlements: ent,
	})
	return b
}

// DecodeSubscriptionEvent reads a subscription change off the bus.
//
// Exported so the gateway can consume it: the account's devices have to learn their
// new entitlements without polling, or a client keeps offering features the server
// has started refusing.
func DecodeSubscriptionEvent(data []byte) (userID string, ent model.Entitlements, ok bool) {
	var ev subscriptionEvent
	if err := json.Unmarshal(data, &ev); err != nil || ev.UserID == "" {
		return "", model.Entitlements{}, false
	}
	return ev.UserID, ev.Entitlements, true
}

var (
	// ErrNoUser means the request named no account.
	ErrNoUser = errors.New("billing: no user")
	// ErrNoIdempotencyKey means the client did not supply one. Required rather than
	// generated: see CheckoutRequest.
	ErrNoIdempotencyKey = errors.New("billing: an idempotency key is required")
	// ErrNoPrice means the plan is not sold in that currency.
	ErrNoPrice = errors.New("billing: no price for this currency")
	// ErrNoProvider means no acquirer is configured for that market.
	ErrNoProvider = errors.New("billing: no payment provider configured")
	// ErrBadMethod means the chosen method is not offered by the acquirer.
	ErrBadMethod = errors.New("billing: payment method not available")
	// ErrChargeFailed means the acquirer refused to start the payment.
	ErrChargeFailed = errors.New("billing: the provider refused the charge")
	// ErrNoProviderRef means the acquirer returned no stable reference, so its
	// callbacks could never be deduplicated.
	ErrNoProviderRef = errors.New("billing: the provider returned no reference")
	// ErrBadSignature means a callback did not authenticate. A webhook endpoint is
	// unauthenticated by construction, so this is the only thing standing between a
	// stranger and a free subscription.
	ErrBadSignature = errors.New("billing: callback signature did not verify")
	// ErrUntrustedSource means a callback arrived from an address the provider does
	// not send notifications from. Refused before any other check.
	ErrUntrustedSource = errors.New("billing: callback from an address the provider does not use")
	// ErrProviderUnavailable means a callback could not be checked because the
	// acquirer itself could not be reached. Unlike ErrBadSignature it is worth a
	// retry: the notification may be perfectly genuine, and answering 400 would
	// make the acquirer give up on a payment the user has already made.
	ErrProviderUnavailable = errors.New("billing: payment provider unavailable")
	// ErrAmountMismatch means a callback reported a different amount than the payment
	// was created for — a provider bug, or a forged body that happened to verify.
	ErrAmountMismatch = errors.New("billing: callback amount does not match the payment")
)

// Provider is one payment method behind one acquirer.
//
// Three methods, and the middle one is the whole service. Charge starts a payment
// and returns somewhere to send the user; Verify interprets a provider callback;
// Refund reverses. Everything difficult is in Verify, because a provider sends its
// callback more than once, out of order, and sometimes for a payment this server
// has already given up on.
type Provider interface {
	// Name identifies the provider in stored rows and logs. Stable: it is written
	// into the payments table and read back to route refunds.
	Name() string
	// Methods lists what this provider can charge (card, sbp, …). A provider may
	// offer several; the checkout picks by (country, currency, method).
	Methods() []model.PaymentMethod

	// Charge begins a payment and returns where to send the user plus the
	// provider's own reference.
	//
	// It takes the idempotency key rather than generating one. The key belongs to
	// the REQUEST, not to the attempt: a client that retries a checkout after a
	// timeout must reach the same payment, and only the caller knows the two
	// requests are the same.
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)

	// Verify authenticates a raw provider callback and extracts what it says.
	//
	// Authentication is not optional and not the caller's job: a webhook endpoint is
	// unauthenticated by construction, so anything that reaches it is an assertion
	// by a stranger until a signature says otherwise. A provider that cannot verify
	// its own callbacks must return an error rather than trusting the body.
	Verify(ctx context.Context, raw []byte, headers map[string]string) (Callback, error)

	// Refund reverses a settled payment, fully or partially.
	Refund(ctx context.Context, providerRef string, amountMinor int64) error
}

// ChargeRequest is what a checkout asks a provider for.
type ChargeRequest struct {
	UserID string
	Plan   model.Plan
	Method model.PaymentMethod
	// AmountMinor is in the currency's minor unit (kopeks, cents). Integers, never
	// floats: a price is an exact quantity and binary floating point cannot hold
	// 0.01, so money in a float64 is a rounding error waiting for a reconciliation
	// meeting.
	AmountMinor int64
	Currency    string
	// IdempotencyKey is the client's, so a retried checkout reaches the same
	// payment instead of creating a second one.
	IdempotencyKey string
	// ReturnURL is where the provider sends the user back to. Empty means the
	// provider's own page.
	ReturnURL string
}

// ChargeResult is where to send the user and what the provider called it.
type ChargeResult struct {
	// ProviderRef is the provider's id for this payment. It is the DEDUPLICATION
	// key for callbacks, so a provider that does not supply one cannot be made
	// idempotent and is not usable here.
	ProviderRef string
	// PayURL is a redirect for card flows. QRPayload is the SBP QR string, which is
	// shown rather than followed — the two are not interchangeable and a client
	// that treats a QR payload as a URL renders a broken link.
	PayURL    string
	QRPayload string
}

// Callback is an authenticated provider notification.
type Callback struct {
	ProviderRef string
	Status      model.PaymentStatus
	AmountMinor int64
	Currency    string
	// PaidAt is the provider's timestamp, not ours. Used for ordering callbacks
	// that arrive out of order.
	PaidAt int64
	// Raw is kept for the audit trail: a disputed charge is settled by what the
	// provider actually said, not by what we recorded it as meaning.
	Raw []byte
}

// Service owns subscriptions and payments.
type Service struct {
	store     store.BillingStore
	providers map[string]Provider
	bus       eventbus.Bus
	ids       *id.Generator
	log       *slog.Logger
	// prices is the plan catalogue, keyed by (plan, currency).
	prices map[priceKey]int64
}

type priceKey struct {
	plan     model.Plan
	currency string
}

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

// SellsTiers reports whether this deployment can actually take money.
//
// The distinction it exists to make is between "billing is wired up" and "billing
// can charge somebody", and conflating those two is how the free tier ends up
// applied where nothing is for sale. The service is constructed whenever a
// BillingStore exists — which is always, since both the Postgres and the
// in-memory store implement it — while an ACQUIRER is genuinely optional: a
// self-hosted instance with no YooKassa or Stripe credentials has a billing
// service and no way for anyone to buy anything.
//
// Callers use it to choose between the free tier and UngatedEntitlements. Getting
// that backwards gates features behind a purchase that cannot be made.
func (s *Service) SellsTiers() bool { return len(s.providers) > 0 }

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
		return nil, fmt.Errorf("%w: %w", ErrChargeFailed, err)
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
	// The source address is checked first, and only for a provider that publishes
	// its own. It is the cheap gate, not the authentication: Verify still runs.
	if r, ok := provider.(SourceRestricted); ok {
		if src := callbackSource(ctx); !r.AllowsSource(src) {
			metrics.WebhookRejected.WithLabelValues(providerName).Inc()
			return fmt.Errorf("%w: %v", ErrUntrustedSource, src)
		}
	}
	cb, err := provider.Verify(ctx, raw, headers)
	if errors.Is(err, ErrProviderUnavailable) {
		// Not a verdict on the callback: we could not ask. Surfaced as-is so the
		// handler answers 500 and the acquirer retries.
		return err
	}
	if err != nil {
		// Logged as a warning and returned: an unverifiable callback is either a
		// misconfiguration or someone probing, and both are worth seeing.
		metrics.WebhookRejected.WithLabelValues(providerName).Inc()
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
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
