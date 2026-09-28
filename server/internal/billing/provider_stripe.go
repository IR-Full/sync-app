package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
)

/*
The rest-of-world acquirer: card.

Two things here differ from the Russian provider in ways worth naming, because they
are the parts that would be wrong if this were written by analogy:

  - Stripe takes form-encoded parameters, not JSON. Sending JSON produces a 400
    that says nothing useful about why.
  - Its webhook signature covers a TIMESTAMP concatenated with the body, and the
    timestamp has to be checked as well as the MAC. A signature that is valid
    forever means a captured callback can be replayed at any point in the future —
    so the tolerance window is part of the verification, not an extra.
*/

// Stripe is the international acquirer.
type Stripe struct {
	// Endpoint is the PaymentIntents API. Configurable so this is testable without
	// reaching the internet.
	Endpoint string
	// SecretKey is the API credential.
	SecretKey string
	// WebhookSecret authenticates callbacks — separate from SecretKey, so a leak in
	// one direction does not compromise the other.
	WebhookSecret string
	HTTP          *http.Client
}

func (s *Stripe) Name() string { return ProviderStripe }

// Methods lists what it can charge. Card only: SBP is a Russian scheme and this
// acquirer has no part in it.
func (s *Stripe) Methods() []model.PaymentMethod {
	return []model.PaymentMethod{model.MethodCard}
}

func (s *Stripe) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: providerTimeout}
}

// Charge creates a payment intent.
func (s *Stripe) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	form := url.Values{}
	// The amount goes in MINOR UNITS, which is what the API wants and what we
	// already hold — no conversion, no rounding, no opportunity for a float.
	form.Set("amount", strconv.FormatInt(req.AmountMinor, 10))
	form.Set("currency", strings.ToLower(req.Currency))
	form.Set("description", "SyncApp "+string(req.Plan))
	form.Set("metadata[user_id]", req.UserID)
	form.Set("metadata[plan]", string(req.Plan))
	form.Set("automatic_payment_methods[enabled]", "true")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return ChargeResult{}, err
	}
	// Form-encoded, not JSON. The API rejects JSON with an error that does not say
	// so, which is a long afternoon if you assume otherwise.
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Authorization", "Bearer "+s.SecretKey)
	// The acquirer deduplicates a retried charge on this, which is the layer our own
	// idempotency key cannot provide: a request whose response we lost may still
	// have created a charge.
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)

	resp, err := s.client().Do(httpReq)
	if err != nil {
		return ChargeResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse))
	if err != nil {
		return ChargeResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ChargeResult{}, fmt.Errorf("stripe: %d: %s", resp.StatusCode, truncate(raw, 512))
	}
	var out struct {
		ID           string `json:"id"`
		ClientSecret string `json:"client_secret"`
		NextAction   struct {
			RedirectToURL struct {
				URL string `json:"url"`
			} `json:"redirect_to_url"`
		} `json:"next_action"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ChargeResult{}, fmt.Errorf("stripe: undecodable response: %w", err)
	}
	payURL := out.NextAction.RedirectToURL.URL
	if payURL == "" {
		// No redirect means the card is confirmed client-side with the intent secret.
		// Carried in PayURL because that is the field a client follows, and inventing
		// a third one would make every client handle a case that is the same case.
		payURL = out.ClientSecret
	}
	return ChargeResult{ProviderRef: out.ID, PayURL: payURL}, nil
}

// Verify authenticates a callback.
//
// The signature covers "<timestamp>.<body>", and the TIMESTAMP is checked as well
// as the MAC. Verifying only the MAC leaves a signature that is valid forever, so a
// callback captured once can be replayed at any point later — the tolerance window
// is part of the verification rather than an optional extra.
func (s *Stripe) Verify(_ context.Context, raw []byte, headers map[string]string) (Callback, error) {
	if s.WebhookSecret == "" {
		return Callback{}, fmt.Errorf("stripe: no webhook secret configured")
	}
	header := headerValue(headers, "Stripe-Signature")
	if header == "" {
		return Callback{}, fmt.Errorf("stripe: callback carried no signature")
	}
	ts, sigs := parseStripeSignature(header)
	if ts == 0 || len(sigs) == 0 {
		return Callback{}, fmt.Errorf("stripe: malformed signature header")
	}
	age := time.Since(time.Unix(ts, 0))
	if age < -webhookTolerance || age > webhookTolerance {
		return Callback{}, fmt.Errorf("stripe: signature timestamp is %v away", age.Round(time.Second))
	}
	mac := hmac.New(sha256.New, []byte(s.WebhookSecret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	ok := false
	for _, got := range sigs {
		// Constant time, and every candidate is checked rather than breaking on the
		// first match: the header may carry several signatures during a secret
		// rotation, and an early break makes the work depend on which one matched.
		if subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1 {
			ok = true
		}
	}
	if !ok {
		return Callback{}, fmt.Errorf("stripe: signature mismatch")
	}

	var ev struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID       string `json:"id"`
				Amount   int64  `json:"amount"`
				Currency string `json:"currency"`
				Status   string `json:"status"`
				Created  int64  `json:"created"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return Callback{}, fmt.Errorf("stripe: undecodable callback: %w", err)
	}
	if ev.Data.Object.ID == "" {
		return Callback{}, fmt.Errorf("stripe: callback named no payment")
	}
	var paidAt int64
	if ev.Data.Object.Created != 0 {
		paidAt = ev.Data.Object.Created * 1000
	}
	return Callback{
		ProviderRef: ev.Data.Object.ID,
		Status:      stripeStatus(ev.Type, ev.Data.Object.Status),
		AmountMinor: ev.Data.Object.Amount,
		Currency:    strings.ToUpper(ev.Data.Object.Currency),
		PaidAt:      paidAt,
		Raw:         raw,
	}, nil
}

func (s *Stripe) Refund(ctx context.Context, providerRef string, amountMinor int64) error {
	form := url.Values{}
	form.Set("payment_intent", providerRef)
	form.Set("amount", strconv.FormatInt(amountMinor, 10))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(s.Endpoint, "/payment_intents")+"/refunds",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+s.SecretKey)
	req.Header.Set("Idempotency-Key", "refund-"+providerRef)

	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse))
		return fmt.Errorf("stripe refund: %d: %s", resp.StatusCode, truncate(raw, 512))
	}
	return nil
}

// parseStripeSignature splits the "t=...,v1=...,v1=..." header.
//
// Several v1 values can appear at once during a secret rotation, so they are
// collected rather than taking the first — otherwise a rotation rejects half the
// callbacks for as long as it lasts.
func parseStripeSignature(header string) (ts int64, sigs []string) {
	for _, part := range strings.Split(header, ",") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch k {
		case "t":
			ts, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			sigs = append(sigs, v)
		}
	}
	return ts, sigs
}

// stripeStatus maps the event vocabulary onto ours.
//
// The EVENT type decides, not the object status: an object can read "succeeded" in
// a `payment_intent.requires_action` event, and treating that as payment would
// grant access before the customer has finished authenticating.
func stripeStatus(eventType, objectStatus string) model.PaymentStatus {
	switch eventType {
	case "payment_intent.succeeded", "charge.succeeded":
		return model.PaySucceeded
	case "payment_intent.payment_failed", "charge.failed":
		return model.PayFailed
	case "charge.refunded", "payment_intent.canceled":
		if eventType == "charge.refunded" {
			return model.PayRefunded
		}
		return model.PayFailed
	default:
		if objectStatus == "succeeded" {
			// An event type we do not recognise carrying a succeeded object: pending,
			// because guessing in the generous direction hands out subscriptions.
			return model.PayPending
		}
		return model.PayPending
	}
}
