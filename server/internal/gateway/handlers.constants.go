package gateway

import "errors"

// errLoginThrottled signals too many auth attempts for a username.
var errLoginThrottled = errors.New("login throttled")

// errBadDisplayName rejects a registration whose display name is not a name.
var errBadDisplayName = errors.New("invalid display name")

// maxIdempotencyKeyLen bounds a checkout idempotency key.
//
// It is stored, indexed and compared, so an unbounded one is a way to put arbitrary
// bytes in a unique index. 128 is far more than any client needs for a UUID.
const maxIdempotencyKeyLen = 128
