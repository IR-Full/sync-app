package gateway

import (
	"net"
	"sync"

	"github.com/IR-Full/sync-app/server/pkg/ratelimit"
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
// unaffected; production sets them via SYNCAPP_MAX_CONNS_PER_IP / _ACCEPT_RATE.
type ipGuard struct {
	rate       *ratelimit.Limiter // nil = no accept-rate limit
	maxPerIP   int                // <= 0 = no concurrent cap
	mu         sync.Mutex
	concurrent map[string]int
}

// newIPGuard builds a guard. acceptRate is new-conns/sec per IP (with a small
// burst); maxPerIP is the concurrent-connection cap per IP. Zero/negative
// disables the respective limit.
func newIPGuard(acceptRate float64, maxPerIP int) *ipGuard {
	g := &ipGuard{maxPerIP: maxPerIP, concurrent: make(map[string]int)}
	if acceptRate > 0 {
		// Allow a short burst (2s worth, min 5) so legitimate parallel tabs/devices
		// behind the same NAT are not rejected on a normal reconnect.
		burst := acceptRate * 2
		if burst < 5 {
			burst = 5
		}
		g.rate = ratelimit.NewLimiter(acceptRate, burst)
	}
	return g
}

// acquire admits a new connection from remote (host:port). It returns the bare
// host key and true if admitted; on rejection it returns false and the caller
// must close the connection without handshaking. release must be called with the
// returned key when an admitted connection ends.
func (g *ipGuard) acquire(remote string) (string, bool) {
	if g == nil {
		return "", true
	}
	host := hostOf(remote)
	if g.rate != nil && !g.rate.Allow(host) {
		return host, false
	}
	if g.maxPerIP > 0 {
		g.mu.Lock()
		if g.concurrent[host] >= g.maxPerIP {
			g.mu.Unlock()
			return host, false
		}
		g.concurrent[host]++
		g.mu.Unlock()
	}
	return host, true
}

// release drops one concurrent connection for host (from a prior acquire).
func (g *ipGuard) release(host string) {
	if g == nil || g.maxPerIP <= 0 || host == "" {
		return
	}
	g.mu.Lock()
	if n := g.concurrent[host]; n <= 1 {
		delete(g.concurrent, host) // bound map size under IP churn
	} else {
		g.concurrent[host] = n - 1
	}
	g.mu.Unlock()
}

// hostOf extracts the IP/host from a "host:port" remote address, falling back to
// the raw string if it has no port (so a missing port never defeats the limit).
func hostOf(remote string) string {
	if h, _, err := net.SplitHostPort(remote); err == nil {
		return h
	}
	return remote
}
