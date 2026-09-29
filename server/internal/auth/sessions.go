package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// Session management and account deletion. "Log out" has to end the session on
// the server: a device that only forgets its token leaves the session valid for
// the rest of its TTL, so a lost phone keeps access. Account deletion is the same
// reasoning applied to the whole account rather than one device.

// ErrWrongPassword is returned when a re-confirmation fails. Distinct from
// ErrBadCredentials so a caller can tell "your password is wrong" (the user can
// fix it) from "this login failed" (which may mean the account is gone).
var ErrWrongPassword = errors.New("auth: password does not match")

// ListSessions returns the caller's LIVE sessions, newest first.
//
// Revoked and expired rows are filtered here rather than in the store: the store
// answers "what rows exist for this user", which the reaper and the audit trail
// both want, while a person asking "where am I signed in" means something
// narrower. Showing them a dead session to click Revoke on would be a lie in
// both directions.
func (s *Service) ListSessions(ctx context.Context, userID string) ([]*model.Session, error) {
	rows, err := s.sessions.ListSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	live := make([]*model.Session, 0, len(rows))
	for _, sess := range rows {
		if validSession(sess) == nil {
			live = append(live, sess)
		}
	}
	return live, nil
}

// RevokeOwned kills one session, but only if it belongs to userID.
//
// The ownership check is the whole point. A session id is not a secret — it
// travels in AUTH_OK and in the session list — so an unscoped revoke would let
// any authenticated account sign out any other by guessing or observing one.
func (s *Service) RevokeOwned(ctx context.Context, userID, sessionID string) error {
	sessions, err := s.sessions.ListSessions(ctx, userID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.ID != sessionID {
			continue
		}
		if sess.RevokedAt != 0 {
			return nil // already gone; revoking twice is not an error
		}
		return s.sessions.RevokeSession(ctx, sessionID, nowMs())
	}
	// Same answer for "no such session" and "someone else's session": telling the
	// caller which would turn the id space into an existence oracle.
	return store.ErrNotFound
}

// RevokeAll kills every live session of an account, optionally sparing one.
//
// keepSessionID is how "sign out everywhere else" is expressed: the caller's own
// connection survives so they are not logged out of the device they are using to
// secure the account. Pass "" to include it.
//
// Returns how many were actually killed, because a client that asked to sign out
// five devices and signed out one should be able to say so.
func (s *Service) RevokeAll(ctx context.Context, userID, keepSessionID string) (int, error) {
	sessions, err := s.ListSessions(ctx, userID)
	if err != nil {
		return 0, err
	}
	now := nowMs()
	revoked := 0
	var firstErr error
	for _, sess := range sessions {
		if sess.ID == keepSessionID {
			continue
		}
		if err := s.sessions.RevokeSession(ctx, sess.ID, now); err != nil {
			// Keep going. Stopping at the first failure would leave a half-signed-out
			// account and report an error, which is the worst of both: the user
			// believes nothing happened and some devices are already out.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		revoked++
	}
	return revoked, firstErr
}

// VerifyPassword re-checks a user's password. Used to gate destructive actions
// on an already-authenticated connection: a session token lives on the device,
// so without this anyone holding an unlocked phone could destroy the account
// behind it.
func (s *Service) VerifyPassword(ctx context.Context, userID, password string) error {
	u, err := s.users.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if !verifyPassword(password, u.PasswordHash) {
		return ErrWrongPassword
	}
	return nil
}

// DeleteAccount erases an account after re-confirming the password.
//
// Sessions are revoked BEFORE the erasure, not after. The store's DeleteAccount
// removes the session rows anyway, but a concurrent connection authenticating in
// the window between the two would otherwise resolve a user that is about to
// stop existing. Revoking first closes that window; if the erasure then fails,
// the account survives with every device signed out — recoverable by logging in
// again, which is the right way round for a failure here to land.
func (s *Service) DeleteAccount(ctx context.Context, userID, password string) error {
	if err := s.VerifyPassword(ctx, userID, password); err != nil {
		return err
	}
	if _, err := s.RevokeAll(ctx, userID, ""); err != nil {
		return fmt.Errorf("auth: could not revoke sessions before deletion: %w", err)
	}
	return s.users.DeleteAccount(ctx, userID)
}
