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
