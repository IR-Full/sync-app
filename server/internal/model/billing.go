package model

/*
Subscriptions, payments and what Premium actually grants.

Money is stored in MINOR UNITS as integers — kopeks, cents — and never as a
float. A price is an exact quantity, binary floating point cannot represent 0.01,
and the error compounds across a ledger until a reconciliation meeting discovers
it. This is the one place in the codebase where the type choice is not a matter of
taste.
*/

// Plan is a subscription tier.
type Plan string

const (
	// PlanFree is the absence of a subscription. It is a named value rather than an
	// empty string so an entitlement lookup for an account that never paid returns
	// something meaningful instead of a zero value that could also mean "unknown".
	PlanFree Plan = "free"
	// PlanPremium is the paid tier.
	PlanPremium Plan = "premium"
)

// PaymentMethod is how the money moves.
type PaymentMethod string

const (
	// MethodCard is a bank card, via whichever acquirer serves the region.
	MethodCard PaymentMethod = "card"
	// MethodSBP is Система быстрых платежей — a Russian instant bank transfer shown
	// as a QR code rather than a redirect. It is a distinct METHOD and not a card
	// variant: the user experience, the acquirer API and the refund path all differ.
	MethodSBP PaymentMethod = "sbp"
)

// SubscriptionStatus is the lifecycle of a subscription.
type SubscriptionStatus string

const (
	// SubIncomplete means a checkout was started and never paid. It exists so a
	// subscription row can be created BEFORE the money arrives — which is what makes
	// the payment callback an update rather than a create, and therefore idempotent.
	SubIncomplete SubscriptionStatus = "incomplete"
	SubActive     SubscriptionStatus = "active"
	// SubPastDue means a renewal failed but access has not been withdrawn yet. A
	// grace state, because a declined card is usually an expired card rather than a
	// decision to stop paying, and cutting access on the first failure loses
	// customers who intended to stay.
	SubPastDue  SubscriptionStatus = "past_due"
	SubCanceled SubscriptionStatus = "canceled"
)

// PaymentStatus is the lifecycle of one payment attempt.
//
// A state machine rather than a boolean, and the ordering matters: a provider
// sends callbacks more than once and out of order, so the service has to be able
// to say "this is not news" and "this is going backwards". See CanTransitionTo.
type PaymentStatus string

const (
	PayPending   PaymentStatus = "pending"
	PaySucceeded PaymentStatus = "succeeded"
	PayFailed    PaymentStatus = "failed"
	PayRefunded  PaymentStatus = "refunded"
)

// CanTransitionTo reports whether a payment may move from one status to another.
//
// This is the guard that makes out-of-order callbacks safe. Providers retry, and
// retries arrive in whatever order the network produces — so a `pending` callback
// showing up after a `succeeded` one is normal, and applying it would un-pay a
// paid subscription. Terminal states are terminal.
func (p PaymentStatus) CanTransitionTo(next PaymentStatus) bool {
	if p == next {
		return false // not an error, but not news either: the caller should do nothing
	}
	switch p {
	case PayPending:
		return next == PaySucceeded || next == PayFailed
	case PaySucceeded:
		// Only a refund. A `failed` callback arriving after `succeeded` is a
		// reordered retry of an earlier attempt, not a reversal.
		return next == PayRefunded
	case PayFailed:
		// A failed payment can still succeed: some methods (SBP especially) report a
		// timeout and then settle. Refusing this would lose money that arrived.
		return next == PaySucceeded
	default:
		return false // refunded is terminal
	}
}

// Terminal reports whether no further transition is possible.
func (p PaymentStatus) Terminal() bool { return p == PayRefunded }

// Subscription is an account's tier over a period.
type Subscription struct {
	UserID string             `json:"user_id"`
	Plan   Plan               `json:"plan"`
	Status SubscriptionStatus `json:"status"`
	// CurrentPeriodEnd is when access lapses without a renewal, in unix millis.
	CurrentPeriodEnd int64 `json:"current_period_end"`
	// Provider is which acquirer holds the payment instrument, so a renewal or a
	// refund goes back to the one that took the money.
	Provider  string `json:"provider,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	// CancelAtPeriodEnd is a cancellation that has not taken effect yet.
	//
	// Cancelling does NOT revoke access immediately: the period is paid for. A
	// product that takes away what was bought the moment someone clicks cancel
	// teaches them not to click it, which is worse for everyone.
	CancelAtPeriodEnd bool `json:"cancel_at_period_end,omitempty"`
}

// Active reports whether the subscription grants Premium at time now (unix millis).
//
// PastDue still grants it. That is the grace state: a declined card is usually an
// expired card, and withdrawing access on the first failure loses people who meant
// to keep paying. The period end is what eventually settles it.
func (s *Subscription) Active(now int64) bool {
	if s == nil || s.Plan != PlanPremium {
		return false
	}
	switch s.Status {
	case SubActive, SubPastDue:
		return s.CurrentPeriodEnd == 0 || s.CurrentPeriodEnd > now
	default:
		return false
	}
}

// Payment is one attempt to collect money.
type Payment struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Plan   Plan   `json:"plan"`
	// Provider and ProviderRef together identify the payment at the acquirer.
	// ProviderRef is the deduplication key for callbacks — a provider that cannot
	// supply a stable one cannot be made idempotent.
	Provider    string        `json:"provider"`
	ProviderRef string        `json:"provider_ref"`
	Method      PaymentMethod `json:"method"`
	AmountMinor int64         `json:"amount_minor"`
	Currency    string        `json:"currency"`
	Status      PaymentStatus `json:"status"`
	// IdempotencyKey is the CLIENT's, so a retried checkout resolves to this row
	// instead of starting a second payment. The same reasoning as a message dedup
	// key, with worse consequences for getting it wrong.
	IdempotencyKey string `json:"idempotency_key"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// Entitlements is what a tier grants, resolved for one account.
//
// A struct of explicit capabilities rather than "is premium", because the gateway
// asks specific questions on specific paths — may this upload be 4 GB, may this
// account open a secret chat — and a boolean forces each of those call sites to
// re-derive the policy. One place decides; everywhere else reads a field.
type Entitlements struct {
	Plan Plan `json:"plan"`
	// SecretChats gates the end-to-end chat type. It is the feature the product
	// leads with, and gating it is a product decision rather than a technical one —
	// which is why it is a field here and not a check inside the crypto.
	SecretChats bool `json:"secret_chats"`
	// MaxUploadBytes is the per-file ceiling. The clearest thing money buys, because
	// it maps directly onto what it costs to serve.
	MaxUploadBytes int64 `json:"max_upload_bytes"`
	// MaxPinnedChats and Folders are UX ceilings that cost the server nothing; they
	// are here because a plan needs reasons to exist beyond the expensive ones.
	MaxPinnedChats int32 `json:"max_pinned_chats"`
	Folders        bool  `json:"folders"`
	// AdvancedSearch enables the filters that are expensive to serve (by sender, by
	// date range, by attachment type).
	AdvancedSearch bool `json:"advanced_search"`
	// PriorityDelivery puts this account's outbound frames in the high-priority QoS
	// lane. The one entitlement that costs nothing to implement, because the lanes
	// already exist.
	PriorityDelivery bool `json:"priority_delivery"`
	// VoiceTranscription is a paid external service, so it is a paid feature.
	VoiceTranscription bool `json:"voice_transcription"`
	// Badge is cosmetic status. Worth listing: a tier with no visible marker is one
	// nobody else knows you have.
	Badge bool `json:"badge"`
}

// FreeEntitlements is what an account gets without paying.
//
// Deliberately usable. A free tier that does not work is not a funnel, it is a
// reason to leave — so the limits are ceilings people rarely reach rather than
// walls they hit on the first day.
func FreeEntitlements() Entitlements {
	return Entitlements{
		Plan:           PlanFree,
		MaxUploadBytes: 100 << 20, // 100 MiB
		MaxPinnedChats: 5,
	}
}

// PremiumEntitlements is what the paid tier grants.
func PremiumEntitlements() Entitlements {
	return Entitlements{
		Plan:               PlanPremium,
		SecretChats:        true,
		MaxUploadBytes:     4 << 30, // 4 GiB
		MaxPinnedChats:     100,
		Folders:            true,
		AdvancedSearch:     true,
		PriorityDelivery:   true,
		VoiceTranscription: true,
		Badge:              true,
	}
}

// EntitlementsFor resolves a subscription to its capabilities at time now.
func EntitlementsFor(sub *Subscription, now int64) Entitlements {
	if sub.Active(now) {
		return PremiumEntitlements()
	}
	return FreeEntitlements()
}

// UngatedEntitlements is what an account gets on a deployment with NO billing at
// all.
//
// Everything, and the distinction from FreeEntitlements is the important one here.
// The free tier is what a non-paying account gets where a paid one is SOLD; it only
// means anything when there is something to buy. A self-hosted instance with no
// acquirer credentials has no tiers, so applying the free tier there would withhold
// features from everybody with no way for anyone to obtain them — a paywall with
// nothing behind it.
//
// Concretely: getting this backwards disables secret chats on every deployment that
// does not take payments, which is most of them. It is a separate named function
// rather than a flag on FreeEntitlements so the two intentions cannot be confused
// at a call site.
func UngatedEntitlements() Entitlements {
	ent := PremiumEntitlements()
	// The plan is still "free": nobody paid, and reporting "premium" would make a
	// client draw a subscription badge and a renewal date for a subscription that
	// does not exist.
	ent.Plan = PlanFree
	ent.Badge = false
	return ent
}
