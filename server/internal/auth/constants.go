package auth

import (
	"errors"
	"time"
)

// SessionTTL is how long a freshly minted session stays valid.
const SessionTTL = 30 * 24 * time.Hour

var (
	// ErrBadCredentials is returned on wrong username/password.
	ErrBadCredentials = errors.New("auth: bad credentials")
	// ErrInvalidSession is returned when a token is unknown, expired, or revoked.
	ErrInvalidSession = errors.New("auth: invalid session")
	// ErrUsernameTaken is returned when registering an existing username.
	ErrUsernameTaken = errors.New("auth: username taken")
)

const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)
