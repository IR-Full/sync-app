package billing

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
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
	// ErrProviderUnavailable means a callback could not be checked because the
	// acquirer itself could not be reached. Unlike ErrBadSignature it is worth a
	// retry: the notification may be perfectly genuine, and answering 400 would
	// make the acquirer give up on a payment the user has already made.
	ErrProviderUnavailable = errors.New("billing: payment provider unavailable")
	// ErrAmountMismatch means a callback reported a different amount than the payment
	// was created for — a provider bug, or a forged body that happened to verify.
	ErrAmountMismatch = errors.New("billing: callback amount does not match the payment")
)
