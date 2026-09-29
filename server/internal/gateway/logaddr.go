package gateway

import (
	"net"
	"strings"

	"github.com/IR-Full/sync-app/server/internal/envcfg"
)

/*
What a connection log is allowed to say about where someone is.

Every connection logged its full source address, and every one of those lines
outlives the connection: logs get shipped, indexed, retained for months and read
by people who have no operational reason to know which building a person opened
the app from. For a messenger that is not incidental metadata — a log of
addresses plus timestamps is a movement history, and it exists whether or not
anyone meant to build one.

The operational need is narrower than the full address. Almost everything a log
is used for — "is one source flooding us", "did this pod see the reconnect
storm", "which network is having trouble" — is answered by the prefix. So the
default truncates: /24 for IPv4, /48 for IPv6, which are the prefixes an abuse
question is actually asked at.

`SYNCAPP_LOG_FULL_IP=1` restores the whole address for the case that genuinely
needs it (an active incident, a report to an upstream provider). It is opt-in and
named for what it does, so turning it on is a decision someone makes rather than
a default they inherit.
*/

// logFullIP reports whether logs may carry a complete source address.
//
// Read once per connection rather than cached in a package variable: the value
// is a deployment decision, and a process that has to restart to change its log
// verbosity is one nobody changes during the incident that needed it.
func logFullIP() bool { return envcfg.Bool("SYNCAPP_LOG_FULL_IP") }

// logAddr renders a remote address for a log line.
//
// Takes "host:port" or a bare host. The port is always dropped: it identifies a
// single socket, tells an operator nothing, and is the part most likely to make
// two lines about the same client look unrelated.
func logAddr(remote string) string {
	if remote == "" {
		return ""
	}
	host := hostOf(remote)
	if logFullIP() {
		return host
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Not an address at all — a unix socket path, or a test harness label.
		// Nothing to truncate, and nothing that identifies a person either.
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String() + "/24"
	}
	return ip.Mask(net.CIDRMask(48, 128)).String() + "/48"
}

// logUser renders a user id for a log line.
//
// A user id is stable and account-identifying, so a log that carries it turns
// into a per-person activity trail by accident. The prefix is enough to
// correlate the lines of ONE connection with each other — which is what the
// field is for — while being useless for following an account across days.
//
// Full ids stay available through the audit log, which is where a record that is
// deliberately about who did what belongs.
func logUser(userID string) string {
	if userID == "" || logFullIP() {
		return userID
	}
	const keep = 4
	if len(userID) <= keep {
		return strings.Repeat("*", len(userID))
	}
	return userID[:keep] + "…"
}
