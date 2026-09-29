package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/IR-Full/sync-app/server/internal/model"
)

// fakeYooAPI serves GET /v3/payments/{id} with whatever status and amount the
// test says the REAL payment has, and records the paths it was asked for.
type fakeYooAPI struct {
	status, paid, amount string
	code                 int
	gets                 atomic.Int64
	lastPath             atomic.Value
}

func (f *fakeYooAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.lastPath.Store(r.URL.Path)
	if u, p, ok := r.BasicAuth(); !ok || u != "shop" || p != "key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		f.gets.Add(1)
	}
	if f.code != 0 {
		w.WriteHeader(f.code)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v3/payments/")
	fmt.Fprintf(w, `{"id":%q,"status":%q,"paid":%s,"amount":{"value":%q,"currency":"RUB"},"captured_at":"2026-09-28T10:00:00Z"}`,
		id, f.status, f.paid, f.amount)
}

func newYoo(t *testing.T, api *fakeYooAPI, webhookSecret string) *YooKassa {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return &YooKassa{Endpoint: srv.URL + "/v3/payments", ShopID: "shop", SecretKey: "key",
		WebhookSecret: webhookSecret, HTTP: srv.Client()}
}

func notification(id, status, amount string) []byte {
	return []byte(fmt.Sprintf(`{"type":"notification","event":"payment.%s","object":{"id":%q,"status":%q,"paid":true,"amount":{"value":%q,"currency":"RUB"}}}`,
		status, id, status, amount))
}

// The attack the old HMAC check was meant to stop, stopped the way YooKassa
// actually allows: a forged "succeeded" for a payment that is really still
// pending yields pending, because only the API's answer is believed.
func TestYooKassaBelievesTheAPINotTheNotification(t *testing.T) {
	api := &fakeYooAPI{status: "pending", paid: "false", amount: "299.00"}
	y := newYoo(t, api, "")
	cb, err := y.Verify(context.Background(), notification("2d1f-aa", "succeeded", "1.00"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cb.Status != model.PayPending {
		t.Fatalf("status = %s; a forged notification decided the outcome", cb.Status)
	}
	if cb.AmountMinor != 29900 {
		t.Fatalf("amount = %d; taken from the notification, not the API", cb.AmountMinor)
	}
}

// The production bug: real notifications carry no signature header, and the old
// Verify rejected every one of them — so no payment was ever applied.
func TestYooKassaAcceptsAnUnsignedGenuineNotification(t *testing.T) {
	api := &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}
	y := newYoo(t, api, "")
	cb, err := y.Verify(context.Background(), notification("2d1f-aa", "succeeded", "299.00"), map[string]string{})
	if err != nil {
		t.Fatalf("a genuine, unsigned notification was rejected: %v", err)
	}
	if cb.Status != model.PaySucceeded || cb.ProviderRef != "2d1f-aa" || cb.PaidAt == 0 {
		t.Fatalf("callback = %+v", cb)
	}
}

// An API we cannot reach says nothing about the notification: it must be retried
// (500), not refused (400) — a 400 makes YooKassa give up on a real payment.
func TestYooKassaUnreachableAPIIsRetryable(t *testing.T) {
	api := &fakeYooAPI{code: http.StatusBadGateway}
	y := newYoo(t, api, "")
	_, err := y.Verify(context.Background(), notification("2d1f-aa", "succeeded", "299.00"), nil)
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}

	svc, _, _ := newSvc(t, y)
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/billing/webhook/"+ProviderYooKassa,
		strings.NewReader(string(notification("2d1f-aa", "succeeded", "299.00")))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("HTTP %d; an unreachable acquirer must be answered 500 so it retries", rec.Code)
	}
}

// A payment the API does not know is not ours to act on: refused, not retried.
func TestYooKassaUnknownPaymentIsRejected(t *testing.T) {
	api := &fakeYooAPI{code: http.StatusNotFound}
	y := newYoo(t, api, "")
	_, err := y.Verify(context.Background(), notification("nope", "succeeded", "299.00"), nil)
	if err == nil || errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want a non-retryable rejection", err)
	}
}

// The id goes into the lookup URL, so anything that could change the path is
// refused before a request is made.
func TestYooKassaRejectsAHostileID(t *testing.T) {
	api := &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}
	y := newYoo(t, api, "")
	for _, id := range []string{"", "../refunds", "a/b", "a?b", "a%2fb", strings.Repeat("a", 65)} {
		if _, err := y.Verify(context.Background(), notification(id, "succeeded", "299.00"), nil); err == nil {
			t.Errorf("id %q was accepted", id)
		}
	}
	if n := api.gets.Load(); n != 0 {
		t.Fatalf("made %d API call(s) for hostile ids", n)
	}
}

// With a secret configured (a signing proxy in front), the signature is still
// required — on top of, not instead of, the API lookup.
func TestYooKassaOptionalSignatureIsEnforcedWhenConfigured(t *testing.T) {
	api := &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}
	y := newYoo(t, api, "proxy-secret")
	body := notification("2d1f-aa", "succeeded", "299.00")

	if _, err := y.Verify(context.Background(), body, map[string]string{"X-Signature": "00"}); err == nil {
		t.Fatal("a wrong signature was accepted")
	}
	mac := hmac.New(sha256.New, []byte("proxy-secret"))
	mac.Write(body)
	good := hex.EncodeToString(mac.Sum(nil))
	if _, err := y.Verify(context.Background(), body, map[string]string{"x-signature": good}); err != nil {
		t.Fatalf("a correct signature was rejected: %v", err)
	}
	if api.gets.Load() != 1 {
		t.Fatalf("API lookups = %d; the signature must not replace the lookup", api.gets.Load())
	}
}

// Refunds live at /v3/refunds; the old code posted to /v3/payments/refunds.
func TestYooKassaRefundHitsTheRefundsEndpoint(t *testing.T) {
	api := &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}
	y := newYoo(t, api, "")
	if err := y.Refund(context.Background(), "2d1f-aa", 29900); err != nil {
		t.Fatal(err)
	}
	if p, _ := api.lastPath.Load().(string); p != "/v3/refunds" {
		t.Fatalf("refund went to %q, want /v3/refunds", p)
	}
}
