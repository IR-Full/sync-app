package call

import "errors"

var (
	// ErrForbidden means the actor is not a member of the chat / not in the call.
	ErrForbidden = errors.New("call: forbidden")
	// ErrCallEnded means the call is no longer joinable.
	ErrCallEnded = errors.New("call: ended")
	// ErrBadKind means an unsupported call kind was requested.
	ErrBadKind = errors.New("call: invalid kind")
)
