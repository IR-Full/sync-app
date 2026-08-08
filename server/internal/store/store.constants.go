package store

import "errors"

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned on unique-constraint violations (e.g. username taken).
var ErrConflict = errors.New("store: conflict")
