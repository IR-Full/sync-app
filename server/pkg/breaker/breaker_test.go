package breaker

import (
	"errors"
	"testing"
	"time"
)

func TestBreakerOpensAndRecovers(t *testing.T) {
	b := New(3, 50*time.Millisecond)
	fail := errors.New("boom")

	// Below threshold: stays closed.
	_ = b.Do(func() error { return fail })
	_ = b.Do(func() error { return fail })
	if b.State() != Closed {
		t.Fatalf("state=%v want Closed", b.State())
	}
	// Third failure opens it.
	_ = b.Do(func() error { return fail })
	if b.State() != Open {
		t.Fatalf("state=%v want Open", b.State())
	}
	// Open rejects without calling fn.
	called := false
	if err := b.Do(func() error { called = true; return nil }); err != ErrOpen || called {
		t.Fatalf("open should reject: err=%v called=%v", err, called)
	}
	// After cooldown, half-open lets one probe through; success closes it.
	time.Sleep(60 * time.Millisecond)
	if err := b.Do(func() error { return nil }); err != nil {
		t.Fatalf("probe should run: %v", err)
	}
	if b.State() != Closed {
		t.Fatalf("state=%v want Closed after recovery", b.State())
	}
}

func TestBreakerHalfOpenReopensOnFailure(t *testing.T) {
	b := New(1, 30*time.Millisecond)
	fail := errors.New("boom")
	_ = b.Do(func() error { return fail }) // opens (threshold 1)
	if b.State() != Open {
		t.Fatal("want Open")
	}
	time.Sleep(40 * time.Millisecond)
	// Probe fails → back to Open.
	_ = b.Do(func() error { return fail })
	if b.State() != Open {
		t.Fatalf("state=%v want Open (probe failed)", b.State())
	}
}
