package replay

import "time"

// maxFrames caps how many recent frames a session buffers.
const maxFrames = 1024

// A buffer is only useful while a client might still reconnect into it, and
// nothing calls Drop on a normal disconnect — the whole point is to survive one.
// So sessions expire: sessionTTL is how long after its last frame a session's
// buffer is kept, and the sweep runs on writes (there is no timer goroutine to
// leak). The Redis backend gets the same behaviour for free from key TTLs.
const (
	sessionTTL  = 10 * time.Minute
	sweepEvery  = time.Minute
	maxSessions = 100_000
)
