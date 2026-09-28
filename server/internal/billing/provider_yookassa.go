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
	"net/url"
	"strings"
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

The shape follows the YooKassa v3 API: JSON POST to /v3/payments with basic auth
and an Idempotence-Key, notifications as {"event", "object": <payment>}.

  - The idempotency key goes in the request, so the acquirer itself deduplicates a
    retried charge. Two layers of idempotency, ours and theirs, because a network
    that ate our response may not have eaten their charge.
  - A notification is NOT trusted for what it says. YooKassa does not sign its
    notifications, so the body is a stranger's claim; the payment is fetched back
    from the API with the shop's credentials and only that answer is believed.
    A forged notification can at most make us re-read a real payment's real state.
    (This used to check an HMAC header that YooKassa never sends, which would have
    rejected every genuine notification in production.)
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
	// WebhookSecret, if set, additionally requires an HMAC-SHA256 of the body in
	// X-Signature — for a deployment that relays notifications through its own
	// signing proxy. YooKassa itself does not sign notifications, so leave it empty
	// when they arrive directly; authenticity then rests on fetching the payment
	// back from the API (see Verify), which does not depend on it.
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

// Verify authenticates a callback by asking the acquirer, and returns what the
// ACQUIRER says about the payment — not what the notification says.
//
// The body is only used to learn which payment to look up. Its status and amount
// are ignored: anyone who knows the webhook URL can POST a notification, and the
// only party whose word counts is the API behind the shop's credentials.
func (y *YooKassa) Verify(ctx context.Context, raw []byte, headers map[string]string) (Callback, error) {
	if y.WebhookSecret != "" {
		sig := headerValue(headers, "X-Signature")
		if sig == "" {
			return Callback{}, fmt.Errorf("yookassa: callback carried no signature")
		}
		mac := hmac.New(sha256.New, []byte(y.WebhookSecret))
		mac.Write(raw)
		want := hex.EncodeToString(mac.Sum(nil))
		// Constant time: an early-exit compare leaks how much of a forged signature
		// is correct, which turns forgery into a guessing game with feedback.
		if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
			return Callback{}, fmt.Errorf("yookassa: signature mismatch")
		}
	}

	var n yooNotification
	if err := json.Unmarshal(raw, &n); err != nil {
		return Callback{}, fmt.Errorf("yookassa: undecodable callback: %w", err)
	}
	if !validYooID(n.Object.ID) {
		return Callback{}, fmt.Errorf("yookassa: callback named no valid payment id")
	}

	p, err := y.fetchPayment(ctx, n.Object.ID)
	if err != nil {
		return Callback{}, err
	}
	amount, err := decimalToMinor(p.Amount.Value)
	if err != nil {
		return Callback{}, fmt.Errorf("yookassa: bad amount %q: %w", p.Amount.Value, err)
	}
	var paidAt int64
	if p.CapturedAt != "" {
		if t, err := time.Parse(time.RFC3339, p.CapturedAt); err == nil {
			paidAt = t.UnixMilli()
		}
	}
	return Callback{
		ProviderRef: p.ID,
		Status:      yooStatus(p.Status, p.Paid),
		AmountMinor: amount,
		Currency:    p.Amount.Currency,
		PaidAt:      paidAt,
		Raw:         raw,
	}, nil
}

// fetchPayment reads a payment from the API with the shop's credentials.
//
// Errors split two ways, and the split decides whether the acquirer retries: an
// unreachable API or a 5xx is ErrProviderUnavailable (answer 500, try again), a
// payment the API does not know is a rejected callback (answer 400, stop).
func (y *YooKassa) fetchPayment(ctx context.Context, id string) (yooPayment, error) {
	// Host and path come from configuration; the id is restricted to
	// [A-Za-z0-9_-] by validYooID before this is called, and escaped anyway.
	// #nosec G704 -- not attacker-steerable beyond one path segment of a fixed host.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(y.Endpoint, "/")+"/"+url.PathEscape(id), nil)
	if err != nil {
		return yooPayment{}, err
	}
	req.SetBasicAuth(y.ShopID, y.SecretKey)
	resp, err := y.client().Do(req) // #nosec G704 -- see above
	if err != nil {
		return yooPayment{}, fmt.Errorf("%w: yookassa: %w", ErrProviderUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse))
	if err != nil {
		return yooPayment{}, fmt.Errorf("%w: yookassa: %w", ErrProviderUnavailable, err)
	}
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return yooPayment{}, fmt.Errorf("%w: yookassa: %d", ErrProviderUnavailable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return yooPayment{}, fmt.Errorf("yookassa: payment %s: %d: %s", id, resp.StatusCode, truncate(body, 256))
	}
	var p yooPayment
	if err := json.Unmarshal(body, &p); err != nil {
		return yooPayment{}, fmt.Errorf("yookassa: undecodable payment: %w", err)
	}
	if p.ID != id {
		return yooPayment{}, fmt.Errorf("yookassa: asked for payment %s, got %s", id, p.ID)
	}
	return p, nil
}

// validYooID accepts the id shapes YooKassa issues (UUID-like) and nothing that
// could change the meaning of the lookup URL.
func validYooID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
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
	// Refunds live beside payments in the API (/v3/refunds), not under them.
	refunds := strings.TrimSuffix(strings.TrimSuffix(y.Endpoint, "/"), "/payments") + "/refunds"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refunds, bytes.NewReader(body))
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

// yooNotification is the callback body. Only the payment id in it is used.
type yooNotification struct {
	Event  string     `json:"event"`
	Object yooPayment `json:"object"`
}

// yooPayment is a payment object as the API returns it.
type yooPayment struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Paid   bool   `json:"paid"`
	Amount struct {
		Value    string `json:"value"`
		Currency string `json:"currency"`
	} `json:"amount"`
	CapturedAt string `json:"captured_at"`
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
