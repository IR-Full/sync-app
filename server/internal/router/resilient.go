package router

import (
	"context"
	"log/slog"
	"time"

	"github.com/synapse-chat/synapse/pkg/breaker"
)

// NewResilient wraps primary with a breaker + local fallback.
func NewResilient(primary Router, log *slog.Logger) Router {
	return &resilientRouter{
		primary: primary,
		local:   NewMemory(),
		br:      breaker.New(5, 5*time.Second),
		log:     log,
	}
}

// Bind writes locally (always) and to the shared router through the breaker.
func (r *resilientRouter) Bind(ctx context.Context, userID, deviceID, nodeID string) error {
	_ = r.local.Bind(ctx, userID, deviceID, nodeID)
	return r.viaBreaker(func() error { return r.primary.Bind(ctx, userID, deviceID, nodeID) }, "bind")
}

func (r *resilientRouter) Unbind(ctx context.Context, userID, deviceID, nodeID string) error {
	_ = r.local.Unbind(ctx, userID, deviceID, nodeID)
	return r.viaBreaker(func() error { return r.primary.Unbind(ctx, userID, deviceID, nodeID) }, "unbind")
}

func (r *resilientRouter) Refresh(ctx context.Context, userID, nodeID string) error {
	_ = r.local.Refresh(ctx, userID, nodeID)
	return r.viaBreaker(func() error { return r.primary.Refresh(ctx, userID, nodeID) }, "refresh")
}

// NodesFor prefers the shared router; on failure/open it degrades to the local
// view (this node's own connections).
func (r *resilientRouter) NodesFor(ctx context.Context, userID string) ([]string, error) {
	if r.br.Allow() {
		nodes, err := r.primary.NodesFor(ctx, userID)
		if err == nil {
			r.br.Success()
			return nodes, nil
		}
		r.br.Failure()
		r.log.Warn("router degraded to local (shared lookup failed)", "err", err)
	}
	return r.local.NodesFor(ctx, userID)
}

func (r *resilientRouter) viaBreaker(fn func() error, op string) error {
	err := r.br.Do(fn)
	if err != nil && err != breaker.ErrOpen {
		r.log.Warn("router shared write failed (degraded)", "op", op, "err", err)
	}
	return nil // writes are best-effort; local already succeeded
}
