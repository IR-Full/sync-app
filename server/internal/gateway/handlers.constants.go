package gateway

import "errors"

// errLoginThrottled signals too many auth attempts for a username.
var errLoginThrottled = errors.New("login throttled")

// errBadDisplayName rejects a registration whose display name is not a name.
var errBadDisplayName = errors.New("invalid display name")
