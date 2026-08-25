package moderation

import (
	"log/slog"
	"sync"

	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/ratelimit"
)

// AbuseEvent is a recorded detection (would persist to abuse_events + audit_logs).
type AbuseEvent struct {
	UserID    string
	ChatID    string
	MessageID string
	Rule      string
	Detail    string
	At        int64
}

// Service applies moderation rules to message events.
type Service struct {
	bus    eventbus.Bus
	log    *slog.Logger
	banned []string
	spam   *ratelimit.Limiter // per-user message velocity
	mu     sync.Mutex
	events []AbuseEvent // in-memory ring for inspection (bounded)
}
