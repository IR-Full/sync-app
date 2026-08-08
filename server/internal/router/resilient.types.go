package router

import (
	"log/slog"

	"github.com/synapse-chat/synapse/pkg/breaker"
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
