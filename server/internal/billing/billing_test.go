package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
)

/*
The billing suite.

Almost nothing here tests "can we take money" — that part is an HTTP call. What is
tested is everything that happens AFTERWARDS, because that is where a payment
integration is actually hard: the acquirer sends its callback more than once, out of
order, sometimes late, sometimes for a payment nobody remembers, and every one of
those has to end in the same state a single well-behaved notification would produce.

The suite is built around a fake provider rather than a mocked HTTP layer, because
the interesting behaviour is in the service and the store, not in JSON shapes.
*/

// fakeProvider is an acquirer under the test's control.
type fakeProvider struct {
	name    string
	methods []model.PaymentMethod
	charges atomic.Int64
	// refPrefix makes the provider reference predictable, so a test can construct a
	// callback for a charge it just made.
	refPrefix  string
	failCharge bool
	noRef      bool
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Methods() []model.PaymentMethod {
	if len(f.methods) > 0 {
		return f.methods
	}
	return []model.PaymentMethod{model.MethodCard}
}

func (f *fakeProvider) Charge(_ context.Context, req ChargeRequest) (ChargeResult, error) {
	n := f.charges.Add(1)
	if f.failCharge {
		return ChargeResult{}, errors.New("acquirer refused")
	}
	if f.noRef {
		return ChargeResult{PayURL: "https://pay.example/x"}, nil
	}
	return ChargeResult{
		// Derived from the idempotency key, which is what a real acquirer does: two
		// requests with one key are one payment at their end too.
		ProviderRef: f.refPrefix + req.IdempotencyKey,
		PayURL:      "https://pay.example/" + strconv.FormatInt(n, 10),
	}, nil
}

func (f *fakeProvider) Verify(_ context.Context, raw []byte, _ map[string]string) (Callback, error) {
	var cb Callback
	if err := json.Unmarshal(raw, &cb); err != nil {
		return Callback{}, err
	}
	if cb.ProviderRef == "" {
		return Callback{}, errors.New("no ref")
	}
	return cb, nil
}

func (f *fakeProvider) Refund(context.Context, string, int64) error { return nil }

func newSvc(t *testing.T, providers ...Provider) (*Service, *memory.Store, *captureBus) {
	t.Helper()
	st := memory.New()
	bus := &captureBus{}
	gen, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(st, bus, gen, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, p := range providers {
		svc = svc.WithProvider(p)
	}
	return svc, st, bus
}

// captureBus records published events so the subscription announcement can be
// asserted on. The announcement is not decoration: without it a client keeps
// offering features the server has started refusing.
type captureBus struct{ events []eventbus.Event }

func (b *captureBus) Publish(_ context.Context, e eventbus.Event) error {
	b.events = append(b.events, e)
	return nil
}
func (b *captureBus) Subscribe(string, string, eventbus.Handler) error { return nil }
func (b *captureBus) Close() error                                     { return nil }

func (b *captureBus) subscriptionEvents() []eventbus.Event {
	var out []eventbus.Event
	for _, e := range b.events {
		if e.Subject == eventbus.SubjSubscription {
			out = append(out, e)
		}
	}
	return out
}

// callback builds a signed-by-the-fake notification.
func callback(ref string, status model.PaymentStatus, amount int64) []byte {
	b, _ := json.Marshal(Callback{
		ProviderRef: ref, Status: status, AmountMinor: amount,
		Currency: "RUB", PaidAt: time.Now().UnixMilli(),
	})
	return b
}

// ------------------------------------------------------------------ checkout

func TestCheckoutCreatesAPendingPayment(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)

	out, err := svc.Checkout(context.Background(), CheckoutRequest{
		UserID: "1", Country: "RU", IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if out.Payment.Status != model.PayPending {
		t.Fatalf("status = %s, want pending", out.Payment.Status)
	}
	if out.Payment.Currency != "RUB" || out.Payment.AmountMinor != 29900 {
		t.Fatalf("priced as %d %s", out.Payment.AmountMinor, out.Payment.Currency)
	}
	if out.PayURL == "" {
		t.Fatal("no payment URL")
	}
	if out.Payment.ProviderRef == "" {
		t.Fatal("the provider reference was not attached; a callback could not find this payment")
	}
}

// TestCheckoutIsIdempotent is the one that matters most in this file: a retried
// checkout must reach the same payment. The thing being duplicated otherwise is
// somebody's money.
func TestCheckoutIsIdempotent(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()
	req := CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "same-key"}

	first, err := svc.Checkout(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Checkout(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Payment.ID != first.Payment.ID {
		t.Fatalf("a retry created payment %s instead of reusing %s",
			second.Payment.ID, first.Payment.ID)
	}
	if !second.Deduplicated {
		t.Error("the retry was not reported as deduplicated")
	}
	if got := p.charges.Load(); got != 1 {
		t.Fatalf("the acquirer was charged %d times for one idempotency key", got)
	}
}

// TestCheckoutRequiresAnIdempotencyKey. A key the server generates differs on every
// retry, which is the same as having none — so it is required rather than defaulted.
func TestCheckoutRequiresAnIdempotencyKey(t *testing.T) {
	svc, _, _ := newSvc(t, &fakeProvider{name: ProviderYooKassa})
	if _, err := svc.Checkout(context.Background(), CheckoutRequest{
		UserID: "1", Country: "RU",
	}); !errors.Is(err, ErrNoIdempotencyKey) {
		t.Fatalf("a checkout with no key returned %v", err)
	}
}

// TestCheckoutKeepsThePaymentRowWhenTheChargeFails.
//
// Deleting it would break the idempotency key: a retry would start a second charge
// at the acquirer, which may well have accepted the first one despite the error we
// saw. A stuck pending row is collected by the expiry path; a double charge is not
// collected by anything.
func TestCheckoutKeepsThePaymentRowWhenTheChargeFails(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, failCharge: true}
	svc, st, _ := newSvc(t, p)
	ctx := context.Background()

	if _, err := svc.Checkout(ctx, CheckoutRequest{
		UserID: "1", Country: "RU", IdempotencyKey: "k1",
	}); !errors.Is(err, ErrChargeFailed) {
		t.Fatalf("want ErrChargeFailed, got %v", err)
	}
	rows, err := st.ListPayments(ctx, "1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d payment rows after a failed charge, want the pending one kept", len(rows))
	}
	if rows[0].Status != model.PayPending {
		t.Fatalf("row status = %s", rows[0].Status)
	}
}

// TestCheckoutRefusesAProviderWithNoReference: a provider whose callbacks cannot be
// deduplicated cannot be made idempotent, and the failure would otherwise appear
// later as duplicate subscriptions.
func TestCheckoutRefusesAProviderWithNoReference(t *testing.T) {
	svc, _, _ := newSvc(t, &fakeProvider{name: ProviderYooKassa, noRef: true})
	if _, err := svc.Checkout(context.Background(), CheckoutRequest{
		UserID: "1", Country: "RU", IdempotencyKey: "k1",
	}); !errors.Is(err, ErrNoProviderRef) {
		t.Fatalf("want ErrNoProviderRef, got %v", err)
	}
}

// TestCheckoutRoutesByCountry: the Russian market gets the acquirer that can do SBP
// and is priced in roubles; everywhere else gets the other one.
func TestCheckoutRoutesByCountry(t *testing.T) {
	yoo := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-",
		methods: []model.PaymentMethod{model.MethodSBP, model.MethodCard}}
	stripe := &fakeProvider{name: ProviderStripe, refPrefix: "stripe-"}
	svc, _, _ := newSvc(t, yoo, stripe)
	ctx := context.Background()

	ru, err := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	if ru.Payment.Provider != ProviderYooKassa || ru.Payment.Currency != "RUB" {
		t.Fatalf("RU routed to %s in %s", ru.Payment.Provider, ru.Payment.Currency)
	}
	// And SBP is offered FIRST there, because it is what most Russian users reach
	// for — a checkout leading with a card form loses conversions to confusion.
	if m := svc.methodsFor("RU"); len(m) == 0 || m[0] != model.MethodSBP {
		t.Fatalf("RU methods = %v, want SBP first", m)
	}

	us, err := svc.Checkout(ctx, CheckoutRequest{UserID: "2", Country: "US", IdempotencyKey: "us"})
	if err != nil {
		t.Fatal(err)
	}
	if us.Payment.Provider != ProviderStripe || us.Payment.Currency != "USD" {
		t.Fatalf("US routed to %s in %s", us.Payment.Provider, us.Payment.Currency)
	}
	de, err := svc.Checkout(ctx, CheckoutRequest{UserID: "3", Country: "DE", IdempotencyKey: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if de.Payment.Currency != "EUR" {
		t.Fatalf("DE priced in %s", de.Payment.Currency)
	}
}

func TestCheckoutRefusesAMethodTheProviderDoesNotOffer(t *testing.T) {
	svc, _, _ := newSvc(t, &fakeProvider{name: ProviderStripe, refPrefix: "s-"})
	// Stripe here offers card only; SBP is a Russian scheme it has no part in.
	if _, err := svc.Checkout(context.Background(), CheckoutRequest{
		UserID: "1", Country: "US", Method: model.MethodSBP, IdempotencyKey: "k",
	}); !errors.Is(err, ErrBadMethod) {
		t.Fatalf("want ErrBadMethod, got %v", err)
	}
}

// ------------------------------------------------------------------ callbacks

// TestSuccessfulCallbackActivatesTheSubscription is the happy path end to end.
func TestSuccessfulCallbackActivatesTheSubscription(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, bus := newSvc(t, p)
	ctx := context.Background()

	out, err := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("an unpaid checkout already granted Premium")
	}

	if err := svc.HandleCallback(ctx, ProviderYooKassa,
		callback(out.Payment.ProviderRef, model.PaySucceeded, 29900), nil); err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	sub := svc.Subscription(ctx, "1")
	if sub.Status != model.SubActive {
		t.Fatalf("status = %s, want active", sub.Status)
	}
	if !sub.Active(time.Now().UnixMilli()) {
		t.Fatal("the subscription is not active after a successful payment")
	}
	ent := svc.Entitlements(ctx, "1")
	if !ent.SecretChats || ent.Plan != model.PlanPremium {
		t.Fatalf("entitlements after payment: %+v", ent)
	}
	// The change is ANNOUNCED. Without it a client keeps its old tier until it
	// happens to ask, and in the meantime offers features the server refuses.
	if n := len(bus.subscriptionEvents()); n == 0 {
		t.Fatal("no subscription event was published")
	}
}

// TestDuplicateCallbackChangesNothingAndIsNotAnError is the single most important
// property here. Providers retry; a retry that gets an error is retried forever, and
// a retry that is APPLIED extends the subscription twice.
func TestDuplicateCallbackChangesNothingAndIsNotAnError(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()
	out, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})
	body := callback(out.Payment.ProviderRef, model.PaySucceeded, 29900)

	if err := svc.HandleCallback(ctx, ProviderYooKassa, body, nil); err != nil {
		t.Fatal(err)
	}
	firstEnd := svc.Subscription(ctx, "1").CurrentPeriodEnd

	for i := 0; i < 3; i++ {
		if err := svc.HandleCallback(ctx, ProviderYooKassa, body, nil); err != nil {
			t.Fatalf("duplicate %d returned an error: %v", i, err)
		}
	}
	if got := svc.Subscription(ctx, "1").CurrentPeriodEnd; got != firstEnd {
		t.Fatalf("duplicates extended the period from %d to %d", firstEnd, got)
	}
}

// TestReorderedCallbackCannotUnpayASubscription. A pending notification arriving
// after a succeeded one is a REORDERED RETRY, not a reversal — applying it would take
// away access somebody paid for.
func TestReorderedCallbackCannotUnpayASubscription(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()
	out, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})
	ref := out.Payment.ProviderRef

	if err := svc.HandleCallback(ctx, ProviderYooKassa, callback(ref, model.PaySucceeded, 29900), nil); err != nil {
		t.Fatal(err)
	}
	// The late arrivals.
	for _, status := range []model.PaymentStatus{model.PayPending, model.PayFailed} {
		if err := svc.HandleCallback(ctx, ProviderYooKassa, callback(ref, status, 29900), nil); err != nil {
			t.Fatalf("a reordered %s callback errored: %v", status, err)
		}
	}
	if !svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("a reordered callback revoked a paid subscription")
	}
}

// TestFailedPaymentCanStillSucceedLater: some methods (SBP especially) report a
// timeout and then settle. Refusing that transition would lose money that arrived.
func TestFailedPaymentCanStillSucceedLater(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()
	out, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})
	ref := out.Payment.ProviderRef

	if err := svc.HandleCallback(ctx, ProviderYooKassa, callback(ref, model.PayFailed, 29900), nil); err != nil {
		t.Fatal(err)
	}
	if svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("a failed payment granted Premium")
	}
	if err := svc.HandleCallback(ctx, ProviderYooKassa, callback(ref, model.PaySucceeded, 29900), nil); err != nil {
		t.Fatal(err)
	}
	if !svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("a late settlement after a reported failure did not activate the plan")
	}
}

// TestCallbackWithTheWrongAmountIsRejected: a notification claiming a different
// number than the payment was created for is a provider bug or a forged body that
// happened to verify. Granting a year of Premium for one kopek is what this one
// comparison prevents.
func TestCallbackWithTheWrongAmountIsRejected(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()
	out, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})

	if err := svc.HandleCallback(ctx, ProviderYooKassa,
		callback(out.Payment.ProviderRef, model.PaySucceeded, 1), nil); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("want ErrAmountMismatch, got %v", err)
	}
	if svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("a mismatched amount activated the subscription")
	}
}

// TestCallbackForAnUnknownPaymentIsNotRetried: the reference is not ours, and
// telling the provider to try again will not make it ours.
func TestCallbackForAnUnknownPaymentIsNotRetried(t *testing.T) {
	svc, _, _ := newSvc(t, &fakeProvider{name: ProviderYooKassa})
	if err := svc.HandleCallback(context.Background(), ProviderYooKassa,
		callback("someone-elses-ref", model.PaySucceeded, 29900), nil); err != nil {
		t.Fatalf("an unknown reference returned %v, want nil (do not retry)", err)
	}
}

// TestRenewalExtendsRatherThanRestartsThePeriod: someone who renews early would
// otherwise lose the days they already paid for, which is a refund request rather
// than a renewal.
func TestRenewalExtendsRatherThanRestartsThePeriod(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()

	first, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})
	if err := svc.HandleCallback(ctx, ProviderYooKassa,
		callback(first.Payment.ProviderRef, model.PaySucceeded, 29900), nil); err != nil {
		t.Fatal(err)
	}
	afterFirst := svc.Subscription(ctx, "1").CurrentPeriodEnd

	second, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k2"})
	if err := svc.HandleCallback(ctx, ProviderYooKassa,
		callback(second.Payment.ProviderRef, model.PaySucceeded, 29900), nil); err != nil {
		t.Fatal(err)
	}
	afterSecond := svc.Subscription(ctx, "1").CurrentPeriodEnd

	if afterSecond <= afterFirst {
		t.Fatalf("the second payment did not extend the period (%d then %d)", afterFirst, afterSecond)
	}
	// And it extended by roughly a period rather than restarting from now.
	if gap := afterSecond - afterFirst; gap < subscriptionPeriod.Milliseconds()-1000 {
		t.Fatalf("the period grew by only %dms, want ~%dms", gap, subscriptionPeriod.Milliseconds())
	}
}

// TestCheckoutWhileActiveDoesNotDowngrade: a renewal must not push a live
// subscription back to `incomplete`, which would take away access somebody is paying
// for while they are in the middle of paying again.
func TestCheckoutWhileActiveDoesNotDowngrade(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()

	first, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})
	_ = svc.HandleCallback(ctx, ProviderYooKassa,
		callback(first.Payment.ProviderRef, model.PaySucceeded, 29900), nil)

	if _, err := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k2"}); err != nil {
		t.Fatal(err)
	}
	if !svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("starting a renewal revoked the live subscription")
	}
}

// ------------------------------------------------------------------ lifecycle

// TestCancelKeepsAccessUntilThePeriodEnds. The period is paid for; a product that
// takes away what was bought the moment someone clicks cancel teaches them not to
// click it.
func TestCancelKeepsAccessUntilThePeriodEnds(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()
	out, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})
	_ = svc.HandleCallback(ctx, ProviderYooKassa,
		callback(out.Payment.ProviderRef, model.PaySucceeded, 29900), nil)

	sub, err := svc.Cancel(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if !sub.CancelAtPeriodEnd {
		t.Fatal("cancel was not recorded")
	}
	if !svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("cancelling revoked access that was already paid for")
	}
}

// TestExpirySweepClosesLapsedSubscriptionsAndAnnouncesThem.
func TestExpirySweepClosesLapsedSubscriptionsAndAnnouncesThem(t *testing.T) {
	svc, st, bus := newSvc(t, &fakeProvider{name: ProviderYooKassa})
	ctx := context.Background()

	past := time.Now().Add(-time.Hour).UnixMilli()
	if err := st.PutSubscription(ctx, &model.Subscription{
		UserID: "1", Plan: model.PlanPremium, Status: model.SubActive,
		CurrentPeriodEnd: past, CreatedAt: past, UpdatedAt: past,
	}); err != nil {
		t.Fatal(err)
	}
	// A live one, to check the sweep is selective.
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := st.PutSubscription(ctx, &model.Subscription{
		UserID: "2", Plan: model.PlanPremium, Status: model.SubActive,
		CurrentPeriodEnd: future, CreatedAt: past, UpdatedAt: past,
	}); err != nil {
		t.Fatal(err)
	}

	svc.sweepExpired(ctx)

	if svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("a lapsed subscription still grants Premium")
	}
	if !svc.Entitlements(ctx, "2").SecretChats {
		t.Fatal("the sweep closed a subscription that had not lapsed")
	}
	// Announced, because the entitlements changed without the client asking.
	if n := len(bus.subscriptionEvents()); n != 1 {
		t.Fatalf("%d subscription events published, want 1", n)
	}
}

// TestPastDueStillGrantsAccess: a declined renewal is usually an expired card rather
// than a decision to stop paying, and cutting access on the first failure loses
// people who meant to stay.
func TestPastDueStillGrantsAccess(t *testing.T) {
	svc, st, _ := newSvc(t, &fakeProvider{name: ProviderYooKassa})
	ctx := context.Background()
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := st.PutSubscription(ctx, &model.Subscription{
		UserID: "1", Plan: model.PlanPremium, Status: model.SubPastDue,
		CurrentPeriodEnd: future,
	}); err != nil {
		t.Fatal(err)
	}
	if !svc.Entitlements(ctx, "1").SecretChats {
		t.Fatal("past_due revoked access inside the paid period")
	}
}

// TestNoSubscriptionIsTheFreeTierNotAnError: every caller wants the TIER, and "no
// row" is a tier. Making each of them translate an error is how one forgets and
// treats the absence as an outage.
func TestNoSubscriptionIsTheFreeTierNotAnError(t *testing.T) {
	svc, _, _ := newSvc(t)
	ent := svc.Entitlements(context.Background(), "nobody")
	if ent.Plan != model.PlanFree || ent.SecretChats {
		t.Fatalf("entitlements for an unknown account: %+v", ent)
	}
	if ent.MaxUploadBytes == 0 {
		t.Fatal("the free tier has no upload ceiling at all; it must be usable")
	}
}

// ------------------------------------------------------------------ money

// TestMoneyConversionsAreExact is the arithmetic test, and it exists because the
// obvious implementation is wrong: formatting minor units through a float64
// introduces an error before the rounding, and the amount that reaches the acquirer
// is occasionally not the amount intended.
func TestMoneyConversionsAreExact(t *testing.T) {
	for _, tc := range []struct {
		minor int64
		text  string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{99, "0.99"},
		{100, "1.00"},
		{29900, "299.00"},
		{29999, "299.99"},
		{123456789, "1234567.89"},
		{-150, "-1.50"},
	} {
		if got := minorToDecimal(tc.minor); got != tc.text {
			t.Errorf("minorToDecimal(%d) = %q, want %q", tc.minor, got, tc.text)
		}
		back, err := decimalToMinor(tc.text)
		if err != nil {
			t.Errorf("decimalToMinor(%q): %v", tc.text, err)
			continue
		}
		if back != tc.minor {
			t.Errorf("decimalToMinor(%q) = %d, want %d", tc.text, back, tc.minor)
		}
	}
}

// TestDecimalToMinorHandlesProviderFormatting: an acquirer sending "10.5" means ten
// fifty, not ten and five kopeks.
func TestDecimalToMinorHandlesProviderFormatting(t *testing.T) {
	for text, want := range map[string]int64{
		"10":      1000,
		"10.5":    1050,
		"10.50":   1050,
		"10.500":  1050,
		" 299.00": 29900,
	} {
		got, err := decimalToMinor(text)
		if err != nil {
			t.Errorf("%q: %v", text, err)
			continue
		}
		if got != want {
			t.Errorf("decimalToMinor(%q) = %d, want %d", text, got, want)
		}
	}
}

// ------------------------------------------------------------------ webhooks

// TestWebhookVerificationIsMandatory covers the real providers rather than the fake
// one, because verification is the only thing between a stranger and a free
// subscription — the endpoint is unauthenticated by construction.
func TestWebhookVerificationIsMandatory(t *testing.T) {
	body := []byte(`{"object":{"id":"p1","status":"succeeded","paid":true,"amount":{"value":"299.00","currency":"RUB"}}}`)

	t.Run("yookassa rejects an unsigned body", func(t *testing.T) {
		y := &YooKassa{WebhookSecret: "shh"}
		if _, err := y.Verify(context.Background(), body, nil); err == nil {
			t.Fatal("an unsigned callback verified")
		}
	})
	t.Run("yookassa rejects a wrong signature", func(t *testing.T) {
		y := &YooKassa{WebhookSecret: "shh"}
		if _, err := y.Verify(context.Background(), body,
			map[string]string{"X-Signature": "00"}); err == nil {
			t.Fatal("a forged signature verified")
		}
	})
	t.Run("yookassa accepts a correct signature", func(t *testing.T) {
		// The signature is the optional proxy layer; the API lookup still decides.
		y := newYoo(t, &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}, "shh")
		mac := hmac.New(sha256.New, []byte("shh"))
		mac.Write(body)
		cb, err := y.Verify(context.Background(), body,
			map[string]string{"X-Signature": hex.EncodeToString(mac.Sum(nil))})
		if err != nil {
			t.Fatalf("a correct signature was rejected: %v", err)
		}
		if cb.ProviderRef != "p1" || cb.Status != model.PaySucceeded {
			t.Fatalf("callback = %+v", cb)
		}
		if cb.AmountMinor != 29900 {
			t.Fatalf("amount = %d, want 29900", cb.AmountMinor)
		}
	})
	t.Run("yookassa refuses when it cannot ask the API", func(t *testing.T) {
		// "We could not check" must never mean "therefore it is fine". With no
		// signing proxy the check IS the API lookup, so an unreachable API refuses
		// — retryably, since the notification may well be genuine.
		y := &YooKassa{}
		_, err := y.Verify(context.Background(), body, nil)
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("err = %v, want ErrProviderUnavailable", err)
		}
	})
	t.Run("header lookup is case-insensitive", func(t *testing.T) {
		// HTTP header names are case-insensitive and different layers canonicalise
		// differently. Missing the signature because of capitalisation fails closed,
		// which is safe and extremely confusing.
		y := newYoo(t, &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}, "shh")
		mac := hmac.New(sha256.New, []byte("shh"))
		mac.Write(body)
		if _, err := y.Verify(context.Background(), body,
			map[string]string{"x-signature": hex.EncodeToString(mac.Sum(nil))}); err != nil {
			t.Fatalf("a lower-cased header name was not found: %v", err)
		}
	})
}

// TestStripeSignatureChecksTheTimestamp. Verifying only the MAC leaves a signature
// valid forever, so a callback captured once could be replayed at any point later.
func TestStripeSignatureChecksTheTimestamp(t *testing.T) {
	const secret = "whsec"
	body := []byte(`{"type":"payment_intent.succeeded","data":{"object":{"id":"pi_1","amount":399,"currency":"usd","status":"succeeded"}}}`)
	sign := func(ts int64) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(strconv.FormatInt(ts, 10)))
		mac.Write([]byte("."))
		mac.Write(body)
		return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
	}
	s := &Stripe{WebhookSecret: secret}

	cb, err := s.Verify(context.Background(), body,
		map[string]string{"Stripe-Signature": sign(time.Now().Unix())})
	if err != nil {
		t.Fatalf("a fresh signature was rejected: %v", err)
	}
	if cb.ProviderRef != "pi_1" || cb.Status != model.PaySucceeded || cb.AmountMinor != 399 {
		t.Fatalf("callback = %+v", cb)
	}

	// An old but correctly-signed body must be refused: that is the replay.
	old := time.Now().Add(-2 * webhookTolerance).Unix()
	if _, err := s.Verify(context.Background(), body,
		map[string]string{"Stripe-Signature": sign(old)}); err == nil {
		t.Fatal("a stale signature verified; a captured callback could be replayed forever")
	}
}

// TestStripeAcceptsSeveralSignaturesDuringRotation: the header carries more than one
// v1 value while a secret is being rotated, and taking only the first rejects half
// the callbacks for as long as the rotation lasts.
func TestStripeAcceptsSeveralSignaturesDuringRotation(t *testing.T) {
	const secret = "whsec"
	body := []byte(`{"type":"payment_intent.succeeded","data":{"object":{"id":"pi_1","amount":399,"currency":"usd"}}}`)
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))

	s := &Stripe{WebhookSecret: secret}
	header := "t=" + strconv.FormatInt(ts, 10) + ",v1=deadbeef,v1=" + good
	if _, err := s.Verify(context.Background(), body,
		map[string]string{"Stripe-Signature": header}); err != nil {
		t.Fatalf("a header with an old and a new signature was rejected: %v", err)
	}
}

// TestWebhookHandlerStatusCodes pins the response codes, which matter more than
// usual here: a provider retries on failure, so the wrong code either produces a
// retry storm or silently drops a valid notification.
func TestWebhookHandlerStatusCodes(t *testing.T) {
	p := &fakeProvider{name: ProviderYooKassa, refPrefix: "yoo-"}
	svc, _, _ := newSvc(t, p)
	ctx := context.Background()
	out, _ := svc.Checkout(ctx, CheckoutRequest{UserID: "1", Country: "RU", IdempotencyKey: "k1"})

	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	post := func(path string, body []byte) int {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	body := callback(out.Payment.ProviderRef, model.PaySucceeded, 29900)
	if code := post("/billing/webhook/"+ProviderYooKassa, body); code != http.StatusOK {
		t.Errorf("a valid callback returned %d", code)
	}
	// A DUPLICATE must be 200, or the provider sends it forever.
	if code := post("/billing/webhook/"+ProviderYooKassa, body); code != http.StatusOK {
		t.Errorf("a duplicate callback returned %d, want 200 so the provider stops", code)
	}
	// An unknown provider is 404 and not retryable.
	if code := post("/billing/webhook/nosuch", body); code != http.StatusNotFound {
		t.Errorf("an unknown provider returned %d, want 404", code)
	}
	// A GET must not reach the verification path at all.
	resp, err := http.Get(srv.URL + "/billing/webhook/" + ProviderYooKassa)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET returned %d, want 405", resp.StatusCode)
	}
}

// TestPaymentStatusTransitions pins the state machine directly. It is the guard that
// makes out-of-order callbacks safe, so it is worth testing without the machinery
// around it.
func TestPaymentStatusTransitions(t *testing.T) {
	for _, tc := range []struct {
		from, to model.PaymentStatus
		want     bool
	}{
		{model.PayPending, model.PaySucceeded, true},
		{model.PayPending, model.PayFailed, true},
		// Not news: a repeat of the same status.
		{model.PayPending, model.PayPending, false},
		{model.PaySucceeded, model.PaySucceeded, false},
		// A reordered retry must not move a paid payment back.
		{model.PaySucceeded, model.PayPending, false},
		{model.PaySucceeded, model.PayFailed, false},
		{model.PaySucceeded, model.PayRefunded, true},
		// A reported failure can still settle (SBP timeouts do this).
		{model.PayFailed, model.PaySucceeded, true},
		// Refunded is terminal.
		{model.PayRefunded, model.PaySucceeded, false},
		{model.PayRefunded, model.PayFailed, false},
	} {
		if got := tc.from.CanTransitionTo(tc.to); got != tc.want {
			t.Errorf("%s -> %s = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

// TestUngatedEntitlementsGrantEverything is the deployment-shape test, and it guards
// a mistake that is easy to make and very visible: applying the FREE tier where
// there is no billing at all would disable secret chats on every self-hosted
// instance, since nobody could ever buy them.
func TestUngatedEntitlementsGrantEverything(t *testing.T) {
	ent := model.UngatedEntitlements()
	if !ent.SecretChats {
		t.Fatal("a deployment with no billing must not gate secret chats: nobody could buy them")
	}
	if !ent.Folders || !ent.AdvancedSearch {
		t.Fatal("ungated entitlements should grant the paid features")
	}
	// But it is still not a paid plan: reporting "premium" would make a client draw
	// a subscription badge and a renewal date for a subscription that does not exist.
	if ent.Plan != model.PlanFree {
		t.Fatalf("plan = %s, want free", ent.Plan)
	}
	if ent.Badge {
		t.Error("an ungated deployment should not show a paid badge")
	}
	// And the free tier, which applies only where a paid one is sold, does gate.
	if model.FreeEntitlements().SecretChats {
		t.Fatal("the free tier should not include secret chats")
	}
}

var _ = store.ErrNotFound // keep the import honest across refactors

// TestSellsTiersDistinguishesAStoreFromAnAcquirer covers the condition that
// decides whether a deployment HAS tiers at all.
//
// It is worth a test of its own because the distinction it draws is the one the
// gateway got wrong for a long time. Every handler asked `svc.Billing == nil`,
// meaning "is billing wired up" — but the service is constructed whenever a
// BillingStore exists, and both the Postgres and the in-memory store implement
// one, so the answer was always "yes" and the ungated branch behind it was dead.
// A self-hosted instance with no acquirer therefore put every account on the free
// tier, which locks secret chats behind a purchase that same instance cannot
// take. `model.UngatedEntitlements` warns about exactly that outcome.
func TestSellsTiersDistinguishesAStoreFromAnAcquirer(t *testing.T) {
	noAcquirer, _, _ := newSvc(t)
	if noAcquirer.SellsTiers() {
		t.Error("a service with no provider claims it can sell; that is the bug this exists to stop")
	}

	withAcquirer, _, _ := newSvc(t, &fakeProvider{})
	if !withAcquirer.SellsTiers() {
		t.Error("a service with a provider says it cannot sell")
	}
}

// TestNoAcquirerOffersNothingToBuy pins the other half: with no provider the
// catalogue lists no way to pay, so a client cannot draw a buy button that would
// fail on click.
func TestNoAcquirerOffersNothingToBuy(t *testing.T) {
	svc, _, _ := newSvc(t)

	for _, offer := range svc.Plans("RU") {
		if len(offer.Methods) != 0 {
			t.Errorf("plan %q offers payment methods with no acquirer: %v", offer.Plan, offer.Methods)
		}
	}
}
