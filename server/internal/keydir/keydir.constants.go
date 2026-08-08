package keydir

import "time"

// opTimeout bounds one directory round trip when the caller's context carries no
// deadline of its own. A backend can be a remote Redis or another process, so a
// client waiting on a prekey fetch must not be able to wait forever because a
// dependency wedged.
const opTimeout = 3 * time.Second
