package schedule

import (
	"errors"
	"time"
)

var (
	// ErrForbidden means the caller may not post to the target chat.
	ErrForbidden = errors.New("schedule: forbidden")
	// ErrPastTime means the requested send time is not in the future.
	ErrPastTime = errors.New("schedule: send time must be in the future")
	// ErrTooFar means the send time exceeds the allowed horizon.
	ErrTooFar = errors.New("schedule: send time too far ahead")
)

// MaxHorizon bounds how far ahead a message may be scheduled. Without a cap, a
// pending table becomes an unbounded, unaudited store of future traffic.
const MaxHorizon = 365 * 24 * time.Hour

// Retention for fired sends. Long enough that "did my scheduled message go?"
// can still be answered from the pending table; short enough that it never
// becomes a duplicate of the message log.
const (
	sentRetain = time.Hour
	purgeEvery = 10 * time.Minute
	purgeBatch = 500
)
