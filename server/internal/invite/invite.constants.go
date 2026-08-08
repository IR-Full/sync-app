package invite

import (
	"errors"
	"regexp"
)

var (
	// ErrForbidden means the actor lacks the required chat rights.
	ErrForbidden = errors.New("invite: forbidden")
	// ErrBadUsername means the handle failed validation.
	ErrBadUsername = errors.New("invite: invalid username")
	// ErrTaken means the handle is already claimed.
	ErrTaken = errors.New("invite: username taken")
	// ErrInvalidLink means the link is missing, revoked, expired, or exhausted.
	ErrInvalidLink = errors.New("invite: link is not usable")
	// ErrLastOwner means demoting this user would leave the chat ownerless.
	ErrLastOwner = errors.New("invite: cannot demote the last owner")
)

// usernameRe bounds handles to a safe, unambiguous alphabet: mixing scripts or
// punctuation into public handles invites look-alike impersonation.
var usernameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{4,31}$`)
