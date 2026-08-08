package notify

import (
	"context"
	"log/slog"

	"github.com/synapse-chat/synapse/pkg/eventbus"
)

// PushJob is the payload fanout publishes on notify.push. Token/Platform are
// filled in by this service when it fans the job out per device — they are not
// part of what fanout knows or should know.
type PushJob struct {
	UserID    string `json:"user_id"`
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	SenderID  string `json:"sender_id"`
	Preview   string `json:"preview"`
	Token     string `json:"-"`
	Platform  string `json:"-"`
}

// Provider delivers a push to a user's devices. Implementations: APNs, FCM,
// WebPush, or LogProvider for dev.
type Provider interface {
	Send(ctx context.Context, job PushJob) error
	Name() string
}

// Service consumes push jobs and dispatches them via a Provider.
type Service struct {
	bus      eventbus.Bus
	provider Provider
	devices  Devices // optional: per-device fan-out + dead-token cleanup
	log      *slog.Logger
}

// deviceSender is a Provider that can address a specific device. A provider
// without it (the dev logger) simply gets the job.
type deviceSender interface {
	SendToDevice(ctx context.Context, dev DeviceToken, job PushJob) error
}

// LogProvider is a dev Provider that logs instead of sending.
type LogProvider struct{ Log *slog.Logger }
