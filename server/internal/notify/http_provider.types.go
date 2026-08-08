package notify

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Devices is the device lookup the push path needs: which tokens does this user
// have, and forget this one when the provider says it is dead.
type Devices interface {
	ListDevices(ctx context.Context, userID string) ([]DeviceToken, error)
	InvalidateToken(ctx context.Context, deviceID string) error
}

// DeviceToken is one push destination.
type DeviceToken struct {
	DeviceID string
	Platform string
	Token    string
}

// HTTPProvider posts a JSON notification to a push endpoint (an FCM/APNs proxy,
// or a self-hosted relay). It is deliberately transport-shaped rather than
// vendor-shaped: the vendor differences that matter here — how a dead token is
// reported and which failures are retryable — are expressed as status codes.
type HTTPProvider struct {
	Endpoint string
	AuthKey  string
	Client   *http.Client
	Log      *slog.Logger
	// MaxAttempts bounds retries of a single push (1 = no retry).
	MaxAttempts int
	// BaseDelay is the first backoff step; each attempt doubles it, with jitter.
	BaseDelay time.Duration
}
