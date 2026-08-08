package poll

import "errors"

// Limits keep a poll a poll — not a free-text broadcast channel.
const (
	MaxQuestionLen = 300
	MaxOptionLen   = 100
	MinOptions     = 2
	MaxOptions     = 10
)

var (
	// ErrForbidden means the actor may not act on this poll/chat.
	ErrForbidden = errors.New("poll: forbidden")
	// ErrClosed means the poll no longer accepts votes.
	ErrClosed = errors.New("poll: closed")
	// ErrBadPoll means the question/options failed validation.
	ErrBadPoll = errors.New("poll: invalid question or options")
	// ErrBadOption means the option index is out of range.
	ErrBadOption = errors.New("poll: option out of range")
)
