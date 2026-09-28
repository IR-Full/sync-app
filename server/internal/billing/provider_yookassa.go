package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
)

/*
The Russian acquirer: bank card and SBP.

SBP (Система быстрых платежей) is a distinct METHOD rather than a card variant,
and the difference runs all the way through: the user is shown a QR code instead
of being redirected, the bank moves the money directly, and the refund path is a
different call. A design that modelled it as "a card with a QR" would have to
special-case it at every step anyway, so it is a method from the start.

The HTTP shape here is deliberately generic — a JSON POST to a configured endpoint
with basic auth, and an HMAC on the callback. Real YooKassa has its own field names
and its own notification format, and adapting to them is a matter of renaming
things in requestBody and yooNotification. What is NOT adaptable, and is therefore
what this file is actually about:

  - The idempotency key goes in the request, so the acquirer itself deduplicates a
    retried charge. Two layers of idempotency, ours and theirs, because a network
    that ate our response may not have eaten their charge.
  - The callback signature is verified with a constant-time compare before anything
    in the body is believed.
  - A notification for an unpaid or cancelled payment is a normal outcome and maps
    to a status rather than to an error.
*/

// YooKassa is the Russian acquirer, offering card and SBP.
type YooKassa struct {
	// Endpoint is the payments API base. Configurable so a deployment can point at
	// a sandbox, and so this is testable without reaching the internet.
	Endpoint string
	// ShopID and SecretKey are the API credentials.
	ShopID    string
	SecretKey string
	// WebhookSecret authenticates callbacks. SEPARATE from SecretKey on purpose: the
	// API credential is used to make outbound calls, the webhook secret to verify
	// inbound ones, and sharing one value between the two means a leak in either
	// direction compromises both.
	WebhookSecret string
	HTTP          *http.Client
}

// Name identifies the provider in stored rows.
func (y *YooKassa) Name() string { return ProviderYooKassa }

// Methods lists what it can charge, SBP first.
//
// Order is not alphabetical: SBP is what most Russian users reach for, and a
// checkout that leads with a card form loses conversions to confusion rather than
// to price.
func (y *YooKassa) Methods() []model.PaymentMethod {
	return []model.PaymentMethod{model.MethodSBP, model.MethodCard}
}

func (y *YooKassa) client() *http.Client {
	if y.HTTP != nil {
		return y.HTTP
	}
	return &http.Client{Timeout: providerTimeout}
}

// Charge starts a payment.
//
// The idempotency key is sent to the acquirer as well as stored locally, and that
// is not redundancy for its own sake: a request whose response we lost may still
// have created a charge, so only the acquirer can tell us whether a retry is a
// retry. Ours stops a second row; theirs stops a second charge.
func (y *YooKassa) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	payload := map[string]any{
		"amount": map[string]any{
			// Sent as a decimal STRING assembled from the integer minor units, never
			// formatted from a float: the value must arrive exactly as intended, and
			// the acquirer will reject or round anything else.
			"value":    minorToDecimal(req.AmountMinor),
			"currency": req.Currency,
		},
		"capture":     true,
		"description": fmt.Sprintf("SyncApp %s", req.Plan),
		"metadata":    map[string]any{"user_id": req.UserID, "plan": string(req.Plan)},
	}
	switch req.Method {
	case model.MethodSBP:
		payload["payment_method_data"] = map[string]any{"type": "sbp"}
	default:
		payload["payment_method_data"] = map[string]any{"type": "bank_card"}
		if req.ReturnURL != "" {
			payload["confirmation"] = map[string]any{"type": "redirect", "return_url": req.ReturnURL}
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChargeResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, y.Endpoint, bytes.NewReader(body))
	if err != nil {
		return ChargeResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotence-Key", req.IdempotencyKey)
	httpReq.SetBasicAuth(y.ShopID, y.SecretKey)

	resp, err := y.client().Do(httpReq)
	if err != nil {
		return ChargeResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse))
	if err != nil {
		return ChargeResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The body is included because an acquirer error message is the only thing
		// that explains a refused charge, and a bare status code sends the operator
		// to the acquirer dashboard to find out what we were already told.
		return ChargeResult{}, fmt.Errorf("yookassa: %d: %s", resp.StatusCode, truncate(raw, 512))
	}
	var out struct {
		ID           string `json:"id"`
		Confirmation struct {
			ConfirmationURL  string `json:"confirmation_url"`
			ConfirmationData string `json:"confirmation_data"`
		} `json:"confirmation"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ChargeResult{}, fmt.Errorf("yookassa: undecodable response: %w", err)
	}
	return ChargeResult{
		ProviderRef: out.ID,
		PayURL:      out.Confirmation.ConfirmationURL,
		// The SBP QR payload. Shown, not followed — a client that renders it as a
		// link produces a broken one.
		QRPayload: out.Confirmation.ConfirmationData,
	}, nil
}

// Verify authenticates a callback and extracts what it says.
//
// The signature check comes FIRST and nothing in the body is read before it
// passes. A webhook endpoint is unauthenticated by construction, so the body is an
// assertion by a stranger until the HMAC says otherwise — and a missing secret is
// a refusal rather than a skip, because "we could not check" must never mean
// "therefore it is fine".
func (y *YooKassa) Verify(_ context.Context, raw []byte, headers map[string]string) (Callback, error) {
	if y.WebhookSecret == "" {
		return Callback{}, fmt.Errorf("yookassa: no webhook secret configured")
	}
	sig := headerValue(headers, "X-Signature")
	if sig == "" {
		return Callback{}, fmt.Errorf("yookassa: callback carried no signature")
	}
	mac := hmac.New(sha256.New, []byte(y.WebhookSecret))
	mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	// Constant time: an early-exit compare leaks how much of a forged signature is
	// correct, which turns forgery into a guessing game with feedback.
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return Callback{}, fmt.Errorf("yookassa: signature mismatch")
	}

	var n yooNotification
	if err := json.Unmarshal(raw, &n); err != nil {
		return Callback{}, fmt.Errorf("yookassa: undecodable callback: %w", err)
	}
	if n.Object.ID == "" {
		return Callback{}, fmt.Errorf("yookassa: callback named no payment")
	}
	amount, err := decimalToMinor(n.Object.Amount.Value)
	if err != nil {
		return Callback{}, fmt.Errorf("yookassa: bad amount %q: %w", n.Object.Amount.Value, err)
	}
	var paidAt int64
	if n.Object.CapturedAt != "" {
		if t, err := time.Parse(time.RFC3339, n.Object.CapturedAt); err == nil {
			paidAt = t.UnixMilli()
		}
	}
	return Callback{
		ProviderRef: n.Object.ID,
		Status:      yooStatus(n.Object.Status, n.Object.Paid),
		AmountMinor: amount,
		Currency:    n.Object.Amount.Currency,
		PaidAt:      paidAt,
		Raw:         raw,
	}, nil
}

// Refund reverses a settled payment.
func (y *YooKassa) Refund(ctx context.Context, providerRef string, amountMinor int64) error {
	body, err := json.Marshal(map[string]any{
		"payment_id": providerRef,
		"amount": map[string]any{
			"value":    minorToDecimal(amountMinor),
			"currency": "RUB",
		},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, y.Endpoint+"/refunds", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// A refund needs its own idempotency key, derived from what is being refunded:
	// a retried refund must not send the money twice, and the acquirer can only
	// dedupe what it is told is the same request.
	req.Header.Set("Idempotence-Key", "refund-"+providerRef)
	req.SetBasicAuth(y.ShopID, y.SecretKey)

	resp, err := y.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse))
		return fmt.Errorf("yookassa refund: %d: %s", resp.StatusCode, truncate(raw, 512))
	}
	return nil
}

// yooNotification is the callback body.
type yooNotification struct {
	Event  string `json:"event"`
	Object struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Paid   bool   `json:"paid"`
		Amount struct {
			Value    string `json:"value"`
			Currency string `json:"currency"`
		} `json:"amount"`
		CapturedAt string `json:"captured_at"`
	} `json:"object"`
}

// yooStatus maps the acquirer vocabulary onto ours.
//
// `waiting_for_capture` maps to pending rather than succeeded, and the distinction
// matters: the money is authorised but not taken, so granting access there would
// hand out a subscription that can still evaporate.
func yooStatus(status string, paid bool) model.PaymentStatus {
	switch status {
	case "succeeded":
		if paid {
			return model.PaySucceeded
		}
		return model.PayPending
	case "canceled":
		return model.PayFailed
	case "waiting_for_capture", "pending":
		return model.PayPending
	default:
		return model.PayPending
	}
}
