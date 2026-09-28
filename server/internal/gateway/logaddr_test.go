package gateway

import "testing"

// A connection log outlives the connection. Addresses plus timestamps are a
// movement history, so the default has to be the truncated form — an operator
// who needs the whole address asks for it.
func TestLogAddrTruncatesByDefault(t *testing.T) {
	cases := map[string]string{
		"203.0.113.47:51234":  "203.0.113.0/24",
		"203.0.113.47":        "203.0.113.0/24",
		"[2001:db8:1:2::9]:0": "2001:db8:1::/48",
		"2001:db8:1:2::9":     "2001:db8:1::/48",
	}
	for remote, want := range cases {
		if got := logAddr(remote); got != want {
			t.Errorf("logAddr(%q) = %q, want %q", remote, got, want)
		}
	}
}

// The port identifies one socket, tells an operator nothing, and is the field
// most likely to make two lines about the same client look unrelated.
func TestLogAddrNeverKeepsThePort(t *testing.T) {
	t.Setenv("SYNCAPP_LOG_FULL_IP", "1")
	if got := logAddr("203.0.113.47:51234"); got != "203.0.113.47" {
		t.Fatalf("got %q, want the bare address", got)
	}
}

func TestLogAddrKeepsTheWholeAddressWhenAskedTo(t *testing.T) {
	t.Setenv("SYNCAPP_LOG_FULL_IP", "1")
	for remote, want := range map[string]string{
		"203.0.113.47:51234":  "203.0.113.47",
		"[2001:db8:1:2::9]:0": "2001:db8:1:2::9",
	} {
		if got := logAddr(remote); got != want {
			t.Errorf("logAddr(%q) = %q, want %q", remote, got, want)
		}
	}
}

// Not everything that reaches this function is an address: a test harness label
// or a unix socket path has nothing to truncate and identifies nobody.
func TestLogAddrPassesThroughNonAddresses(t *testing.T) {
	for _, s := range []string{"", "pipe", "/tmp/syncapp.sock"} {
		if got := logAddr(s); got != s {
			t.Errorf("logAddr(%q) = %q, want it unchanged", s, got)
		}
	}
}

// A user id is stable and account-identifying, so a log carrying it becomes a
// per-person activity trail by accident. The prefix still correlates the lines
// of one connection, which is what the field is for.
func TestLogUserShortensByDefault(t *testing.T) {
	const id = "7321456789012345"
	got := logUser(id)
	if got == id {
		t.Fatal("the full user id reached the log")
	}
	if len(got) > 8 {
		t.Fatalf("logUser kept too much: %q", got)
	}
	// Still enough to tell two connections apart within one log.
	if logUser("7321456789012345") == logUser("9999456789012345") {
		t.Fatal("logUser collapsed two different accounts to the same label")
	}
}

func TestLogUserHandlesShortAndEmptyIDs(t *testing.T) {
	if got := logUser(""); got != "" {
		t.Errorf("logUser(\"\") = %q", got)
	}
	// A short id has no prefix worth keeping; masking it entirely is better than
	// printing the whole thing because it is short.
	if got := logUser("ab"); got != "**" {
		t.Errorf("logUser(\"ab\") = %q, want it fully masked", got)
	}
}

func TestLogUserKeepsTheWholeIDWhenAskedTo(t *testing.T) {
	t.Setenv("SYNCAPP_LOG_FULL_IP", "1")
	const id = "7321456789012345"
	if got := logUser(id); got != id {
		t.Fatalf("got %q, want the full id", got)
	}
}
