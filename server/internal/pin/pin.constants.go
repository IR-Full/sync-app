package pin

import "errors"

var (
	// ErrForbidden means the user may not pin/unpin here (or is not a member).
	ErrForbidden = errors.New("pin: forbidden")
	// ErrTooLong means the draft exceeds the size cap.
	ErrTooLong = errors.New("pin: draft too long")
)

// MaxDraftLen bounds a draft so the table cannot become free storage.
const MaxDraftLen = 8192

// SyncPageSize bounds one draft-sync response, for the same reason contact sync
// is bounded: a draft carries text, so an unbounded page is a frame-sized
// response waiting to happen.
const SyncPageSize = 200
