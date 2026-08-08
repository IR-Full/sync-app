package gateway

import "testing"

func TestIPGuardConcurrentCap(t *testing.T) {
	g := newIPGuard(0, 2) // no rate limit; cap 2 concurrent per IP

	h1, ok := g.acquire("10.0.0.1:1000")
	if !ok {
		t.Fatal("first connection should be admitted")
	}
	if _, ok := g.acquire("10.0.0.1:1001"); !ok {
		t.Fatal("second connection should be admitted")
	}
	if _, ok := g.acquire("10.0.0.1:1002"); ok {
		t.Fatal("third connection from same IP should be rejected (cap=2)")
	}
	// A different IP is unaffected by the first IP's usage.
	if _, ok := g.acquire("10.0.0.2:1000"); !ok {
		t.Fatal("different IP should be admitted")
	}
	// Releasing one frees a slot for the capped IP.
	g.release(h1)
	if _, ok := g.acquire("10.0.0.1:1003"); !ok {
		t.Fatal("after release a slot should be free")
	}
}

func TestIPGuardAcceptRate(t *testing.T) {
	// rate 1/s, burst = max(rate*2, 5) = 5. The 6th immediate accept is throttled.
	g := newIPGuard(1, 0)
	admitted := 0
	for i := 0; i < 10; i++ {
		if _, ok := g.acquire("10.0.0.9:5000"); ok {
			admitted++
		}
	}
	if admitted != 5 {
		t.Fatalf("expected 5 admitted within burst, got %d", admitted)
	}
}

func TestIPGuardNilDisabled(t *testing.T) {
	var g *ipGuard // both limits off → guard is nil
	if _, ok := g.acquire("1.2.3.4:5"); !ok {
		t.Fatal("nil guard must admit everything")
	}
	g.release("1.2.3.4") // must not panic
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1:1234":     "10.0.0.1",
		"[2001:db8::1]:443": "2001:db8::1",
		"noport":            "noport",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q)=%q want %q", in, got, want)
		}
	}
}
