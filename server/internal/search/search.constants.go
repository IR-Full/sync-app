package search

// Result-set bounds. The limit is what the caller may see, not what the index may
// scan — the scope is applied first, so these numbers are about frame size and
// screen space rather than about work.
const (
	defaultLimit = 20
	maxLimit     = 50
)
