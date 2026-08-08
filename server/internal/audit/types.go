package audit

import (
	"context"
	"log/slog"
)

// Event is one audited action. Actor is who did it, Target what it touched.
type Event struct {
	At     int64
	Action string
	Actor  string
	Target string
	Detail string
}

// Sink persists audit events. Implementations must be append-only.
type Sink interface {
	Record(ctx context.Context, e Event)
}

// LogSink writes audit events to a structured logger under a dedicated "audit"
// channel, so they can be routed to an immutable store by the log pipeline.
type LogSink struct{ Log *slog.Logger }
