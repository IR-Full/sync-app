package media

import (
	"context"
	"log/slog"
	"time"

	"github.com/synapse-chat/synapse/internal/metrics"
)

// Blobs outlive nothing on their own: an upload is stored before the message
// that references it exists, and it stays after that message is deleted. So the
// object store needs a collector, and the collector needs one question answered —
// "does any live message still point at this?" — which only the message log can
// answer.
//
// Two paths feed it. The prompt one runs when a message is deleted: the sender
// asked for the content to go, and waiting a sweep interval to honour that is a
// poor answer for a self-destructing message. The patient one is the sweep, which
// catches everything the prompt path cannot see — uploads that were never
// attached (ticket issued, bytes PUT, message never sent) and blobs freed by the
// self-destruct reaper, which clears its refs in bulk inside the store.
//
// Both go through the same check, because a forwarded message carries a COPY of
// the original's ref: deleting the original must not blank the forward.

// WithReferencer enables collection. Without one the service still serves
// uploads and downloads; it simply never deletes, which is the old behaviour.
func (s *Service) WithReferencer(r Referencer) *Service { s.refs = r; return s }

// DeleteIfUnreferenced removes each blob that no live message points at. Refs
// that are still referenced are left alone — a forward shares the original's
// ref, so "the sender deleted their copy" is not "the bytes are unreachable".
func (s *Service) DeleteIfUnreferenced(ctx context.Context, reason string, refs ...string) {
	if s.refs == nil {
		return
	}
	for _, ref := range refs {
		if ref == "" || !s.store.Exists(ref) {
			continue
		}
		used, err := s.refs.MediaRefExists(ctx, ref)
		if err != nil {
			s.log.Warn("media reference check failed; keeping the object", "ref", ref, "err", err)
			continue
		}
		if used {
			continue
		}
		if err := s.store.Delete(ref); err != nil {
			s.log.Warn("media delete failed", "ref", ref, "err", err)
			continue
		}
		metrics.MediaDeleted.WithLabelValues(reason).Inc()
	}
}

// SweepOrphans collects stored objects old enough to have been attached by now
// that no live message references. Returns how many went.
func (s *Service) SweepOrphans(ctx context.Context) (int, error) {
	if s.refs == nil {
		return 0, nil
	}
	lister, ok := s.store.(Lister)
	if !ok {
		return 0, nil // a backend that cannot enumerate cannot be swept
	}
	refs, err := lister.ListOlderThan(time.Now().Add(-gcMinAge))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, ref := range refs {
		select {
		case <-ctx.Done():
			return n, ctx.Err()
		default:
		}
		used, err := s.refs.MediaRefExists(ctx, ref)
		if err != nil {
			s.log.Warn("media reference check failed; keeping the object", "ref", ref, "err", err)
			continue
		}
		if used {
			continue
		}
		if err := s.store.Delete(ref); err != nil {
			s.log.Warn("media delete failed", "ref", ref, "err", err)
			continue
		}
		metrics.MediaDeleted.WithLabelValues("orphan").Inc()
		n++
	}
	return n, nil
}

// RunGC sweeps on a ticker until ctx is cancelled.
func (s *Service) RunGC(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = gcEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := s.SweepOrphans(ctx)
			if err != nil && ctx.Err() == nil {
				s.log.Warn("media sweep failed", "err", err)
			}
			if n > 0 {
				s.log.Info("collected orphaned media", "count", n)
			}
		}
	}
}

// WithLogger sets the logger used by the collector (default: discard).
func (s *Service) WithLogger(l *slog.Logger) *Service { s.log = l; return s }
