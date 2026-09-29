// Package ratelimit is a lightweight token-bucket limiter used for flood/abuse
// control (Section 14). A bucket refills at a steady rate up to a burst
// capacity; each admitted action costs one token. It is allocation-light and
// lock-guarded so it can front the gateway's per-connection message loop and the
// auth service's per-IP login attempts.
package ratelimit

import (
	"sync"
	"time"
)

// Bucket is a single token bucket.
type Bucket struct {
	mu       sync.Mutex
	capacity float64
	tokens   float64
	refill   float64 // tokens per second
	last     time.Time
}

// Limiter is a keyed set of buckets (e.g. per user id or per IP). Idle buckets
// are garbage-collected lazily to bound memory under churn.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*entry
	rate    float64
	burst   float64
	ttl     time.Duration
	lastGC  time.Time
}

type entry struct {
	b        *Bucket
	lastSeen time.Time
}

// NewBucket creates a bucket that refills at ratePerSec up to burst capacity,
// starting full.
func NewBucket(ratePerSec, burst float64) *Bucket {
	return &Bucket{capacity: burst, tokens: burst, refill: ratePerSec, last: time.Now()}
}

// Allow reports whether one token is available, consuming it if so.
func (b *Bucket) Allow() bool { return b.AllowN(1) }

// AllowN consumes n tokens if available.
func (b *Bucket) AllowN(n float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.refill
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

// NewLimiter builds a keyed limiter.
func NewLimiter(ratePerSec, burst float64) *Limiter {
	return &Limiter{
		buckets: make(map[string]*entry),
		rate:    ratePerSec,
		burst:   burst,
		ttl:     10 * time.Minute,
		lastGC:  time.Now(),
	}
}

// Allow admits one action for key.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	e, ok := l.buckets[key]
	if !ok {
		e = &entry{b: NewBucket(l.rate, l.burst)}
		l.buckets[key] = e
	}
	e.lastSeen = time.Now()
	l.maybeGC()
	l.mu.Unlock()
	return e.b.Allow()
}

// maybeGC drops idle buckets; caller holds the lock.
func (l *Limiter) maybeGC() {
	now := time.Now()
	if now.Sub(l.lastGC) < l.ttl {
		return
	}
	l.lastGC = now
	for k, e := range l.buckets {
		if now.Sub(e.lastSeen) > l.ttl {
			delete(l.buckets, k)
		}
	}
}
