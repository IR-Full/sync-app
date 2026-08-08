package e2e

import "errors"

// ErrIdentityChanged means a peer device presented a different identity key than
// the one pinned for it. It carries both keys so a client can show the safety
// numbers side by side.
var ErrIdentityChanged = errors.New("e2e: peer identity key changed since it was pinned")
