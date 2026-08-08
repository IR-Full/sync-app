package outbox

import "time"

// Retention. The outbox is a HANDOFF table, not an archive: each row carries a
// full copy of the message it announces, so a table that is only ever appended to
// ends up bigger than the message log itself. Published rows are kept briefly —
// long enough to inspect a delivery that just happened, not long enough to become
// a second copy of the system's data.
const (
	defaultRetain     = 10 * time.Minute
	defaultPurgeEvery = time.Minute
	defaultPurgeBatch = 1000
)
