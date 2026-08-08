// Package breaker is a small circuit breaker for calls to external dependencies
// (Redis, Postgres). When a dependency starts failing, the breaker "opens" and
// callers stop hammering it — they fast-fail or fall back to a degraded local
// path — until a cooldown lets a few probe calls test recovery. This turns a
// dependency outage into graceful degradation instead of a pile-up of blocked
// goroutines and timeouts.
package breaker

import (
	"time"
)

// New builds a breaker that opens after `threshold` consecutive failures and
// stays open for `cooldown` before probing.
func New(threshold int, cooldown time.Duration) *Breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 5 * time.Second
	}
	return &Breaker{threshold: threshold, cooldown: cooldown, halfOpenMax: 1}
}

// Allow reports whether a call may proceed, advancing state (Open→HalfOpen after
// cooldown).
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Closed:
		return true
	case Open:
		if time.Since(b.openedAt) >= b.cooldown {
			b.state = HalfOpen
			b.halfOpen = 0
			return true
		}
		return false
	default: // HalfOpen
		if b.halfOpen < b.halfOpenMax {
			b.halfOpen++
			return true
		}
		return false
	}
}

// Success records a successful call.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	if b.state == HalfOpen {
		b.state = Closed
	}
}

// Failure records a failed call, opening the breaker at the threshold.
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.state == HalfOpen || (b.state == Closed && b.failures >= b.threshold) {
		b.state = Open
		b.openedAt = time.Now()
	}
}

// Do runs fn under the breaker: it returns ErrOpen without calling fn when open,
// otherwise runs fn and records the outcome.
func (b *Breaker) Do(fn func() error) error {
	if !b.Allow() {
		return ErrOpen
	}
	err := fn()
	if err != nil {
		b.Failure()
	} else {
		b.Success()
	}
	return err
}

// State returns the current state (for metrics/inspection).
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
