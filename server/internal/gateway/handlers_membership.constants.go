package gateway

import "time"

// Bounds on a create. The title cap keeps a chat name a name; the member cap
// keeps creation from being a bulk-add primitive that skips the invite path
// (where rate limits and link accounting live). Neither is a scale limit —
// channels grow by joining, which is the flow that is designed for it.
const (
	maxChatTitle       = 128
	maxCreateMembers   = 200
	maxCreateResolveIn = 8 * time.Second
)
