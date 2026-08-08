package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/synapse-chat/synapse/internal/metrics"
)

// The push path is where a messenger's reliability becomes visible to someone
// who is not looking at it: a missed push is a message the user never learns
// about. Three things make that path trustworthy, and none of them are the
// transport itself.
//
//	PER DEVICE, NOT PER USER. A user is a set of devices, each with its own
//	token. Sending "to a user" is not a thing a provider can do.
//
//	RETRY WITH BACKOFF, BUT ONLY WHERE IT HELPS. A 5xx or a timeout is worth
//	retrying; a rejected payload is not, and retrying it just multiplies the
//	failure. Jitter keeps a provider blip from turning into a synchronized
//	retry storm from every worker at once.
//
//	DEAD TOKENS MUST DIE. Providers report an unregistered token explicitly
//	(APNs 410, FCM UNREGISTERED/404). Keeping it means every future message to
//	that user pays for a delivery that cannot happen, forever. The token is
//	removed on the spot.

// NewHTTPProvider builds a provider with sane transport defaults. A push must
// never outlive the usefulness of the notification it carries, so the client
// timeout is short.
func NewHTTPProvider(endpoint, authKey string, log *slog.Logger) *HTTPProvider {
	return &HTTPProvider{
		Endpoint:    endpoint,
		AuthKey:     authKey,
		Client:      &http.Client{Timeout: 5 * time.Second},
		Log:         log,
		MaxAttempts: 3,
		BaseDelay:   200 * time.Millisecond,
	}
}

// Name identifies the provider.
func (p *HTTPProvider) Name() string { return "http" }

// Send is the Provider interface: it delivers to one device.
func (p *HTTPProvider) Send(ctx context.Context, job PushJob) error {
	return p.SendToDevice(ctx, DeviceToken{Token: job.Token, Platform: job.Platform}, job)
}

// pushPayload is what the endpoint receives. message_id is included so the
// provider (and the device) can dedup: delivery is at-least-once by design.
type pushPayload struct {
	Token     string `json:"token"`
	Platform  string `json:"platform,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
}

// SendToDevice delivers one notification, retrying only failures that a retry
// can fix.
func (p *HTTPProvider) SendToDevice(ctx context.Context, dev DeviceToken, job PushJob) error {
	body, err := json.Marshal(pushPayload{
		Token: dev.Token, Platform: dev.Platform,
		Title: "New message", Body: job.Preview,
		ChatID: job.ChatID, MessageID: job.MessageID,
	})
	if err != nil {
		return err
	}
	attempts := p.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	delay := p.BaseDelay
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		retryable, err := p.post(ctx, body)
		if err == nil {
			metrics.PushSent.Inc()
			return nil
		}
		lastErr = err
		if !retryable {
			return err
		}
		if attempt == attempts {
			break
		}
		// Jittered backoff: without the jitter, every worker that hit the same
		// provider blip retries in lockstep and reproduces it.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay + time.Duration(rand.Int63n(int64(delay/2+1)))): // #nosec G404 -- jitter, not a secret
		}
		delay *= 2
	}
	metrics.PushFailed.WithLabelValues("exhausted").Inc()
	return lastErr
}

// post performs one attempt and classifies the outcome: (retryable, error).
func (p *HTTPProvider) post(ctx context.Context, body []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.AuthKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.AuthKey)
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		metrics.PushFailed.WithLabelValues("transport").Inc()
		return true, err // network/timeouts are exactly what retries are for
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10)) // reuse the connection
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound:
		// APNs 410 / FCM 404: the app was uninstalled or the token rotated.
		metrics.PushFailed.WithLabelValues("unregistered").Inc()
		return false, errTokenDead
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		metrics.PushFailed.WithLabelValues("provider").Inc()
		return true, fmt.Errorf("notify: provider status %d", resp.StatusCode)
	default:
		// 4xx that is not a dead token: a malformed payload or bad credentials.
		// Retrying multiplies the failure without changing it.
		metrics.PushFailed.WithLabelValues("rejected").Inc()
		return false, fmt.Errorf("notify: rejected with status %d", resp.StatusCode)
	}
}
