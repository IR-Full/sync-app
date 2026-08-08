// Package outbox contains the relay that drains the transactional outbox to the
// event bus. Producers (the message write path) stage events in the outbox table
// inside the message's own transaction; this relay reads unsent rows, publishes
// them, and marks them sent. That closes the "commit succeeded but publish was
// lost on crash" gap — delivery becomes durable at-least-once. Consumers must be
// idempotent (they already dedup by message id).
//
// The MVP relay polls on a short interval. Production would use Postgres
// LISTEN/NOTIFY or logical decoding (CDC) to avoid polling latency.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/eventbus"
)

// New builds a relay.
func New(s store.OutboxStore, bus eventbus.Bus, log *slog.Logger) *Relay {
	return &Relay{
		store: s, bus: bus, log: log, interval: 20 * time.Millisecond, batch: 200,
		retain: defaultRetain, purgeEvery: defaultPurgeEvery, purgeBatch: defaultPurgeBatch,
	}
}

// WithRetention overrides how long published rows are kept and how often they
// are collected (tests use a short window; operators may want a longer one to
// keep a forensic trail).
func (r *Relay) WithRetention(retain, every time.Duration) *Relay {
	if retain > 0 {
		r.retain = retain
	}
	if every > 0 {
		r.purgeEvery = every
	}
	return r
}

// Run drains the outbox until ctx is cancelled. If the store supports
// LISTEN/NOTIFY it wakes on notifications (with a slow fallback tick); otherwise
// it polls on the short interval.
func (r *Relay) Run(ctx context.Context) {
	var notify <-chan struct{}
	interval := r.interval
	if l, ok := r.store.(Listener); ok {
		if ch, err := l.Listen(ctx); err == nil {
			notify = ch
			interval = time.Second // NOTIFY drives latency; tick is just a safety net
			r.log.Info("outbox relay using LISTEN/NOTIFY")
		} else {
			r.log.Warn("outbox LISTEN failed; polling", "err", err)
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	purge := time.NewTicker(r.purgeEvery)
	defer purge.Stop()
	r.drain(ctx) // drain anything already staged at startup
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.drain(ctx)
		case <-notify:
			r.drain(ctx)
		case <-purge.C:
			r.purge(ctx)
		}
	}
}

// purge collects published rows in bounded chunks. It shares the relay's
// goroutine deliberately: collection must never outrun or contend with the
// delivery it exists to clean up after, and interleaving the two here makes that
// structural rather than a matter of tuning.
func (r *Relay) purge(ctx context.Context) {
	before := time.Now().Add(-r.retain).UnixMilli()
	for {
		n, err := r.store.PurgeSent(ctx, before, r.purgeBatch)
		if err != nil {
			r.log.Warn("outbox purge failed", "err", err)
			return
		}
		if n > 0 {
			metrics.RowsPurged.WithLabelValues("outbox").Add(float64(n))
		}
		if n < r.purgeBatch {
			return // caught up
		}
		select { // a large backlog must not starve delivery
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (r *Relay) drain(ctx context.Context) {
	for {
		recs, err := r.store.Poll(ctx, r.batch)
		if err != nil {
			r.log.Warn("outbox poll failed", "err", err)
			return
		}
		if len(recs) == 0 {
			return
		}
		metrics.OutboxBatch.Observe(float64(len(recs)))
		sent := make([]string, 0, len(recs))
		for _, rec := range recs {
			if err := r.bus.Publish(ctx, eventbus.Event{Subject: rec.Subject, Key: rec.Key, Data: rec.Data, Headers: rec.Trace}); err != nil {
				r.log.Warn("outbox publish failed", "subject", rec.Subject, "err", err)
				break // stop on first failure; retry the rest next tick
			}
			sent = append(sent, rec.ID)
			metrics.OutboxPublished.Inc()
		}
		if len(sent) > 0 {
			if err := r.store.MarkSent(ctx, sent); err != nil {
				r.log.Warn("outbox mark-sent failed", "err", err)
				return
			}
		}
		if len(recs) < r.batch {
			return // drained
		}
	}
}
