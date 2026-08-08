package breaker

import (
	"sync"
	"time"
)

// State is the breaker's state.
type State int

// Breaker is a thread-safe circuit breaker.
type Breaker struct {
	mu          sync.Mutex
	state       State
	failures    int
	threshold   int
	cooldown    time.Duration
	openedAt    time.Time
	halfOpenMax int
	halfOpen    int
}
