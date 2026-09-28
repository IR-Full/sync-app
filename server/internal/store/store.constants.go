package store

import "errors"

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned on unique-constraint violations (e.g. username taken).
var ErrConflict = errors.New("store: conflict")

// ErrPollClosed is returned by Vote when the poll stopped accepting votes.
//
// The check belongs in the store, not above it: a caller that reads `closed` and
// then writes a vote leaves a window in which a concurrent close lands between
// the two, so the vote is accepted into a closed poll. Only the store can hold
// the row and the insert in one transaction.
var ErrPollClosed = errors.New("store: poll closed")

// ErrUnsupported is returned when a backend does not implement an OPTIONAL
// capability the caller needs.
//
// Distinct from ErrNotFound: "this store cannot answer that" is an operator's
// configuration problem, while "no such row" is a normal outcome. A caller that
// conflated them would report a missing feature as missing data.
var ErrUnsupported = errors.New("store: unsupported by this backend")
