package gateway

import (
	"sync"

	"github.com/synapse-chat/synapse/pkg/ratelimit"
)

// ipGuard blunts connection-flood and reconnect-storm attacks at the accept
// boundary, before a connection ever reaches the handshake. It enforces two
// independent limits per source IP:
//
//   - an accept RATE limit (token bucket): caps new connections/sec from one IP,
//     so a single host cannot spin the accept loop or exhaust FDs by reconnecting
//     in a tight loop.
//   - a concurrent CONNECTION cap: bounds how many live connections one IP may
//     hold at once, so one host cannot occupy a large share of the node.
//
// Both default off (limit <= 0) so single-process dev and the test suite are
// unaffected; production sets them via SYNAPSE_MAX_CONNS_PER_IP / _ACCEPT_RATE.
type ipGuard struct {
	rate       *ratelimit.Limiter // nil = no accept-rate limit
	maxPerIP   int                // <= 0 = no concurrent cap
	mu         sync.Mutex
	concurrent map[string]int
}
