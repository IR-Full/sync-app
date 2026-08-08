// Package audit is the append-only audit log (Sections 6, 14). It records
// security-relevant events — logins, admin actions, chat exports, moderation,
// permission denials — as an immutable trail. The MVP writes structured log
// records (shippable to a WORM store / SIEM); a DB-backed sink implementing the
// same Sink interface persists them for compliance queries.
package audit

import (
	"context"
	"log/slog"
	"time"
)

// NewLogSink builds a log-backed audit sink.
func NewLogSink(log *slog.Logger) *LogSink { return &LogSink{Log: log} }

// Record appends an event.
func (s *LogSink) Record(_ context.Context, e Event) {
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}

	s.Log.Info("AUDIT",
		"audit", true,
		"at", e.At,
		"action", e.Action,
		"actor", e.Actor,
		"target", e.Target,
		"detail", e.Detail,
	)
}
