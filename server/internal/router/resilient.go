package router

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/IR-Full/sync-app/server/pkg/breaker"
)

// resilientRouter wraps a shared (Redis) router with a circuit breaker and a
// local in-memory fallback. When Redis is healthy, routing is cluster-wide as
// usual. When Redis starts failing, the breaker opens and the node falls back to
// its LOCAL view: it still binds/looks up its own connected users, so messages
// to same-node recipients keep flowing (cross-node delivery degrades to
// history-sync on reconnect). This turns a Redis outage into partial degradation
// instead of a total delivery outage.
type resilientRouter struct {
	primary Router // shared (Redis)
	local   Router // in-memory, this node's own binds
	br      *breaker.Breaker
	log     *slog.Logger
}

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

// NodesForMany mirrors NodesFor for a batch: shared view first, local fallback.
//
// The fallback is all-or-nothing on purpose. A partial answer stitched from both
// views would report some recipients offline because Redis was unreachable and
// others online because they happen to be here — and the caller turns "offline"
// into a push notification, so a half-answer sends duplicate pushes to people
// who received the message.
func (r *resilientRouter) NodesForMany(ctx context.Context, userIDs []string) (map[string][]string, error) {
	if r.br.Allow() {
		nodes, err := r.primary.NodesForMany(ctx, userIDs)
		if err == nil {
			r.br.Success()
			return nodes, nil
		}
		r.br.Failure()
		r.log.Warn("router degraded to local (shared batch lookup failed)", "err", err)
	}
	return r.local.NodesForMany(ctx, userIDs)
}

// NodesForDevice mirrors NodesFor: shared view first, local fallback on failure.
// The fallback is this node's own connections, which is exactly the set that
// still matters when the shared registry is unreachable — a device connected
// here is reachable here regardless of what Redis can tell us.
func (r *resilientRouter) NodesForDevice(ctx context.Context, userID, deviceID string) ([]string, error) {
	if r.br.Allow() {
		nodes, err := r.primary.NodesForDevice(ctx, userID, deviceID)
		if err == nil {
			r.br.Success()
			return nodes, nil
		}
		r.br.Failure()
		r.log.Warn("router degraded to local (shared device lookup failed)", "err", err)
	}
	return r.local.NodesForDevice(ctx, userID, deviceID)
}

func (r *resilientRouter) viaBreaker(fn func() error, op string) error {
	err := r.br.Do(fn)
	if err != nil && !errors.Is(err, breaker.ErrOpen) {
		r.log.Warn("router shared write failed (degraded)", "op", op, "err", err)
	}
	return nil // writes are best-effort; local already succeeded
}
