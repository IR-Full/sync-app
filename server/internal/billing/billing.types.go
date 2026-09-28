package billing

import (
	"context"
	"log/slog"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
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
