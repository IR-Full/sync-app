package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/SyncApp-chat/SyncApp/internal/store"
)

/*
Changing a password.

Without it a leaked password is a permanently lost account: RevokeAll kills
every session, and whoever knows the password signs in again a minute later —
two claimants with identical credentials. What follows is the change itself plus
the two things that have to happen alongside the write and are easy to leave out.
*/

// ChangePassword replaces the caller's password after re-confirming the old one,
// then signs out every OTHER session. Returns how many were killed.
//
// Three deliberate choices:
//
//   - The old password is required. A session token is enough to act as the
//     account but not enough to REPLACE its credential: a stolen token would
//     otherwise become permanent ownership, which is the one thing a password
//     change is supposed to take back.
//   - Other sessions are revoked. A password change is nearly always a response
//     to suspecting someone else has access, and leaving their sessions alive
//     makes the change cosmetic.
//   - The caller's own session survives. Signing someone out of the device they
//     are using to secure the account is a good way to have them not finish.
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword, keepSessionID string) (int, error) {
	setter, ok := s.users.(store.PasswordStore)
	if !ok {
		return 0, store.ErrUnsupported
	}
	if len(newPassword) < MinPasswordLen {
		return 0, fmt.Errorf("%w: password must be at least %d characters",
			ErrWeakPassword, MinPasswordLen)
	}
	u, err := s.users.GetUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	if !verifyPassword(oldPassword, u.PasswordHash) {
		return 0, ErrWrongPassword
	}
	// Rejected rather than accepted as a no-op: someone who types their current
	// password into "new password" has misread the form, and silently succeeding
	// tells them the change happened.
	if verifyPassword(newPassword, u.PasswordHash) {
		return 0, ErrSamePassword
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return 0, err
	}
	if err := setter.SetPasswordHash(ctx, userID, hash); err != nil {
		return 0, err
	}
	// The revoke happens AFTER the write, and a failure here is reported rather
	// than swallowed: the password is already changed, so the caller needs to know
	// that the other sessions were not actually closed — that is the difference
	// between "you are safe now" and "change it again from another device".
	n, err := s.RevokeAll(ctx, userID, keepSessionID)
	if err != nil {
		return 0, fmt.Errorf("password changed but sessions were not revoked: %w", err)
	}
	return n, nil
}

// ErrWeakPassword and friends are distinct from ErrBadCredentials on purpose: a
// password change reports a fixable mistake in a form the user is looking at,
// while ErrBadCredentials sends a client to a login screen.
var (
	// ErrWeakPassword means the new password does not meet the minimum.
	ErrWeakPassword = errors.New("auth: password too weak")
	// ErrSamePassword means the new password equals the current one.
	ErrSamePassword = errors.New("auth: new password matches the current one")
)
