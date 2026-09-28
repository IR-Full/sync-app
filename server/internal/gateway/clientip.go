package gateway

import (
	"net"
	"net/http"
	"strings"
)

/*
Resolving the client address behind a proxy.

The per-IP accept guard keyed on `RemoteAddr`, which is correct only when clients
connect to this process directly. Behind a load balancer or ingress — which is
where anything with a TLS certificate actually runs — every connection arrives
from the proxy's address, and the guard degrades into one of two useless shapes:
either the caps are high enough that a flood passes, or they are low enough that
the proxy hits them and the whole site is rejected.

The fix is not "read X-Forwarded-For". That header is client-supplied and trivial
to forge, and trusting it blindly is worse than ignoring it: an attacker sends a
fresh fake address per connection and gets an unlimited budget, plus the ability
to attribute their traffic to someone else's IP. It is only meaningful when the
hop it came from is one we put there.

So: the header is consulted ONLY when the peer is a configured trusted proxy, and
the address taken from it is the RIGHTMOST one that is not itself trusted. The
right-hand end of the list is what our own infrastructure appended; everything to
the left of the first untrusted entry is whatever the client chose to send, and
is discarded.

NOT covered: the raw TCP and QUIC listeners. They speak a binary protocol with no
header to carry a forwarded address, so an L4 proxy in front of them needs the
PROXY protocol, which this server does not implement. Putting one there today
silently returns the guard to counting a single address — documented in
SECURITY.md rather than left to be discovered.
*/

// parseTrustedProxies turns configuration strings into networks. Accepts CIDRs
// ("10.0.0.0/8") and bare addresses ("192.0.2.7", treated as a /32 or /128).
// Anything unparseable is dropped and reported, because silently ignoring a
// malformed entry would leave an operator believing a proxy is trusted when it
// is not.
func parseTrustedProxies(entries []string) (nets []*net.IPNet, bad []string) {
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, n)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		bad = append(bad, entry)
	}
	return nets, bad
}

// isTrustedProxy reports whether addr is one of the hops we placed ourselves.
func isTrustedProxy(addr string, trusted []*net.IPNet) bool {
	if len(trusted) == 0 {
		return false
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the address the accept guard should charge this request to.
//
// With no trusted proxies configured — the default, and the right default — this
// is just the peer address, and a forged header changes nothing.
func clientIP(remoteAddr string, header http.Header, trusted []*net.IPNet) string {
	peer := hostOf(remoteAddr)
	if !isTrustedProxy(peer, trusted) {
		return peer
	}

	// Walk right to left: the rightmost entry was appended by the hop nearest us.
	// Keep stepping over entries that are themselves trusted proxies, and stop at
	// the first that is not — that is the closest address we have any reason to
	// believe. Everything further left is client-controlled.
	forwarded := header.Values("X-Forwarded-For")
	var chain []string
	for _, value := range forwarded {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				chain = append(chain, part)
			}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		candidate := hostOf(chain[i])
		if net.ParseIP(candidate) == nil {
			// A malformed entry means the chain cannot be trusted past this point;
			// stop rather than skip, or a garbage entry would let an attacker steer
			// which address is picked.
			break
		}
		if !isTrustedProxy(candidate, trusted) {
			return candidate
		}
	}

	// Every hop in the chain is ours, or there is no chain: the proxy itself is
	// the closest thing to a client address we have.
	return peer
}
