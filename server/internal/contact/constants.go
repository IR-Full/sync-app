package contact

import (
	"errors"

	"github.com/synapse-chat/synapse/internal/store"
)

var (
	// ErrSelf means the user tried to add or block themselves.
	ErrSelf = errors.New("contact: cannot target yourself")
	// ErrBadName means the local name failed validation.
	ErrBadName = errors.New("contact: invalid name")
	// ErrNotFound is returned when the target user does not exist.
	ErrNotFound = store.ErrNotFound
)

// MaxNameLen bounds the local label a user may attach to a contact.
const MaxNameLen = 100

// SyncPageSize bounds one sync response. An address book is unbounded in
// principle, and a full sync used to be a single frame — a large one lands on
// the protocol's 16 MiB ceiling and fails as a whole instead of degrading. The
// client repeats the request with the cursor it gets back until a page comes
// back empty.
const SyncPageSize = 500
