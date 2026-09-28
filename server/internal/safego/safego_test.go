package safego

import (
	"testing"
	"time"
)

func TestRecoverContainsPanic(t *testing.T) {
	done := make(chan struct{})
	Go(nil, "test.recover", func() {
		defer close(done)
		panic("boom")
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never ran")
	}
	// Reaching here at all is the assertion: an unrecovered panic in that
	// goroutine would have taken the test binary down with it.
}

func TestLoopRestartsAfterPanic(t *testing.T) {
	done := make(chan struct{})
	attempts := make(chan int, 4)
	n := 0
	Loop(done, nil, "test.loop", func() {
		n++
		attempts <- n
		if n < 3 {
			panic("transient")
		}
		<-done // third entry behaves like a real loop and blocks
	})
	defer close(done)

	for want := 1; want <= 3; want++ {
		select {
		case got := <-attempts:
			if got != want {
				t.Fatalf("attempt %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("loop did not restart into attempt %d", want)
		}
	}
}

func TestLoopStopsWhenFnReturns(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	calls := make(chan struct{}, 4)
	Loop(done, nil, "test.loop.clean", func() { calls <- struct{}{} })

	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("loop never ran")
	}
	// A clean return means "this loop is finished", so it must not be re-entered.
	select {
	case <-calls:
		t.Fatal("loop restarted after a clean return")
	case <-time.After(restartDelay + 500*time.Millisecond):
	}
}
