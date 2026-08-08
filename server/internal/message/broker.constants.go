package message

import "errors"

// The Broker is the single write-side entry point for message mutations. It
// lives in the message service (not a separate microservice) on purpose: create,
// edit, and delete all act on the same aggregate and must share one transaction
// (per-chat seq allocation + insert + transactional-outbox event are atomic).
// Fronting that with a network service would mean a distributed transaction for
// no benefit. The Broker instead centralizes the cross-cutting write concerns —
// validation, tracing, metrics, and uniform error handling — behind one typed
// command interface, giving a clean seam if the write path is ever peeled off.

// MaxTextLen bounds a message's text (generous vs Telegram's 4096). The frame
// cap (16 MiB) is a transport guard; this is the domain rule.
const MaxTextLen = 8192

var (
	// ErrEmptyMessage means a create/edit carried neither text nor media.
	ErrEmptyMessage = errors.New("message: empty (no text or media)")
	// ErrTooLong means the text exceeds MaxTextLen.
	ErrTooLong = errors.New("message: text too long")
	// ErrBadCommand means an unknown or malformed command.
	ErrBadCommand = errors.New("message: bad command")
)

const (
	OpCreate Op = "create"
	OpEdit   Op = "edit"
	OpDelete Op = "delete"
)
