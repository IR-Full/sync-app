package gateway

import (
	"net/http"
	"testing"
)

func headerWith(values ...string) http.Header {
	h := http.Header{}
	for _, v := range values {
		h.Add("X-Forwarded-For", v)
	}
	return h
}

// The default — no trusted proxies — must ignore the header completely. This is
// the case that matters most, because it is what a direct deployment runs, and a
// forged header there would hand any client an unlimited per-IP budget.
func TestClientIPIgnoresForwardedHeaderWithoutTrustedProxies(t *testing.T) {
	got := clientIP("203.0.113.9:44321", headerWith("1.2.3.4"), nil)
	if got != "203.0.113.9" {
		t.Fatalf("a forged header changed the address: %q", got)
	}
}

// Same, but the peer simply is not on the trust list: an untrusted hop's claim
// about who it is forwarding for carries no weight at all.
func TestClientIPIgnoresForwardedHeaderFromAnUntrustedPeer(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.0/8"})
	got := clientIP("203.0.113.9:44321", headerWith("1.2.3.4"), trusted)
	if got != "203.0.113.9" {
		t.Fatalf("an untrusted peer was believed: %q", got)
	}
}

func TestClientIPUsesForwardedAddressFromATrustedProxy(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.0/8"})
	got := clientIP("10.0.0.7:5000", headerWith("198.51.100.42"), trusted)
	if got != "198.51.100.42" {
		t.Fatalf("want the forwarded client, got %q", got)
	}
}

// The rightmost entry is the one our own hop appended; everything to its left is
// whatever the client chose to send. Taking the LEFTMOST — the obvious reading of
// "the original client" — is exactly the bug that lets an attacker pick their own
// address by prepending one.
func TestClientIPTakesTheRightmostUntrustedEntry(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.0/8"})
	forged := headerWith("1.1.1.1, 2.2.2.2, 198.51.100.42")
	if got := clientIP("10.0.0.7:5000", forged, trusted); got != "198.51.100.42" {
		t.Fatalf("a client-supplied prefix won: %q", got)
	}
}

// A real chain has our own hops at the right-hand end; they must be stepped over
// to reach the client, but not past the first address that is not ours.
func TestClientIPSkipsOurOwnHops(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.0/8", "172.16.0.0/12"})
	chain := headerWith("198.51.100.42, 172.16.4.4, 10.0.0.9")
	if got := clientIP("10.0.0.7:5000", chain, trusted); got != "198.51.100.42" {
		t.Fatalf("want the client behind our hops, got %q", got)
	}
}

// Split across several header lines, which is legal and what some proxies do.
func TestClientIPHandlesRepeatedHeaders(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.0/8"})
	chain := headerWith("198.51.100.42", "10.0.0.9")
	if got := clientIP("10.0.0.7:5000", chain, trusted); got != "198.51.100.42" {
		t.Fatalf("repeated headers mishandled: %q", got)
	}
}

// A malformed entry stops the walk rather than being skipped. Skipping would let
// an attacker insert garbage to steer which address is picked.
func TestClientIPStopsAtAMalformedEntry(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.0/8"})
	chain := headerWith("198.51.100.42, not-an-ip, 10.0.0.9")
	if got := clientIP("10.0.0.7:5000", chain, trusted); got != "10.0.0.7" {
		t.Fatalf("a malformed chain was walked past: %q", got)
	}
}

// Every hop is ours and there is no client entry: the proxy is the closest thing
// to a client address available, and inventing one would be worse.
func TestClientIPFallsBackToThePeerWhenTheChainIsAllOurs(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.0/8"})
	if got := clientIP("10.0.0.7:5000", headerWith("10.0.0.9"), trusted); got != "10.0.0.7" {
		t.Fatalf("want the peer, got %q", got)
	}
	if got := clientIP("10.0.0.7:5000", http.Header{}, trusted); got != "10.0.0.7" {
		t.Fatalf("want the peer with no header, got %q", got)
	}
}

func TestClientIPHandlesIPv6(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"2001:db8::/32"})
	got := clientIP("[2001:db8::1]:5000", headerWith("2001:db8:ffff::9, 2001:db8::2"), trusted)
	// 2001:db8:ffff::9 is inside 2001:db8::/32, so every hop is trusted and the
	// peer wins — the same fallback as the IPv4 case, which is the point of
	// asserting it: the trust test must not quietly stop working on v6.
	if got != "2001:db8::1" {
		t.Fatalf("ipv6 chain mishandled: %q", got)
	}

	trusted6, _ := parseTrustedProxies([]string{"2001:db8::/64"})
	got = clientIP("[2001:db8::1]:5000", headerWith("2001:db8:ffff::9"), trusted6)
	if got != "2001:db8:ffff::9" {
		t.Fatalf("want the v6 client, got %q", got)
	}
}

func TestParseTrustedProxiesAcceptsCIDRsAndBareAddresses(t *testing.T) {
	nets, bad := parseTrustedProxies([]string{"10.0.0.0/8", " 192.0.2.7 ", "2001:db8::1", ""})
	if len(bad) != 0 {
		t.Fatalf("rejected valid entries: %v", bad)
	}
	if len(nets) != 3 {
		t.Fatalf("want 3 networks, got %d", len(nets))
	}
	if !isTrustedProxy("192.0.2.7", nets) {
		t.Error("a bare address did not become a host route")
	}
	if isTrustedProxy("192.0.2.8", nets) {
		t.Error("a bare address matched its neighbour")
	}
}

// A typo must be reported, not silently dropped: an operator who believes their
// ingress is trusted and finds it is not has a guard counting the wrong address
// with nothing in the log to say why.
func TestParseTrustedProxiesReportsGarbage(t *testing.T) {
	nets, bad := parseTrustedProxies([]string{"10.0.0.0/8", "10.0.0.0/33", "definitely-not-an-ip"})
	if len(nets) != 1 {
		t.Fatalf("want 1 usable network, got %d", len(nets))
	}
	if len(bad) != 2 {
		t.Fatalf("want 2 rejected entries, got %v", bad)
	}
}
