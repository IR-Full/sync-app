// Handlers for session management and account deletion. They make "log out" end
// the session on the server rather than only on the device, and let an account
// created in-app also be destroyed in-app.
package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// handleSessionList answers "where am I signed in".
//
// Scoped to the authenticated account with no addressable target: there is no
// field for whose sessions to list, because a message that had one would be a
// cross-account enumeration primitive and no client needs it.
func (c *conn) handleSessionList(ctx context.Context, e wire.Envelope) error {
	sessions, err := c.gw.svc.Auth.ListSessions(ctx, c.userID)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}

	out := wire.SessionsBody{Sessions: make([]wire.SessionInfo, 0, len(sessions))}
	for _, s := range sessions {
		out.Sessions = append(out.Sessions, wire.SessionInfo{
			SessionID: s.ID,
			DeviceID:  s.DeviceID,
			Platform:  c.platformOf(ctx, s.DeviceID),
			CreatedAt: s.CreatedAt,
			ExpiresAt: s.ExpiresAt,
			Current:   s.ID == c.sessionID,
		})
	}
	return c.reply(wire.MsgSessions, e.RequestID, out)
}

// platformOf resolves a device id to the platform string it announced at HELLO.
//
// Best-effort on purpose: the platform is a label to help a person recognise a
// device in a list ("android", "web"), so a device row that has gone missing
// costs a blank field rather than the whole listing. Failing the request because
// one of five devices could not be described would be the wrong trade.
func (c *conn) platformOf(ctx context.Context, deviceID string) string {
	if deviceID == "" || c.gw.svc.Users == nil {
		return ""
	}
	d, err := c.gw.svc.Users.GetDevice(ctx, deviceID)
	if err != nil || d == nil || d.UserID != c.userID {
		return ""
	}
	return d.Platform
}

// handleSessionRevoke kills one session, or all of them.
//
// Three shapes, and the difference between the last two is deliberate rather
// than accidental:
//
//   - a session id                  → that one session
//   - no id                         → every session EXCEPT this connection's
//   - no id + all_including_current → every session, this one included
//
// "Sign out everywhere else" is what a person reaches for after losing a device,
// and signing them out of the phone they are holding while they do it would be
// actively unhelpful. "Sign out everywhere" is a different intention, so it gets
// a field rather than being expressed by omitting one.
func (c *conn) handleSessionRevoke(ctx context.Context, e wire.Envelope) error {
	var body wire.SessionRevokeBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad session revoke")
	}

	if body.SessionID != "" {
		if err := c.gw.svc.Auth.RevokeOwned(ctx, c.userID, body.SessionID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return c.replyError(e.RequestID, wire.ErrNotFound, "no such session")
			}
			return c.replyForError(e.RequestID, err)
		}
		c.gw.audit(ctx, "session.revoke", c.userID, body.SessionID, "")
		self := body.SessionID == c.sessionID
		done := wire.SessionRevokedBody{Revoked: 1, Self: self}
		if self {
			// Revoking the session you are holding: say so, then go. Leaving the
			// socket up would keep serving a session the user just killed.
			return c.replyThenClose(wire.MsgSessionRevoked, e.RequestID, done)
		}
		return c.reply(wire.MsgSessionRevoked, e.RequestID, done)
	}

	keep := c.sessionID
	if body.AllIncludingCurrent {
		keep = ""
	}
	revoked, err := c.gw.svc.Auth.RevokeAll(ctx, c.userID, keep)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "session.revoke_all", c.userID, "", auditScope(body.AllIncludingCurrent))
	done := wire.SessionRevokedBody{Revoked: revoked, Self: body.AllIncludingCurrent}
	if body.AllIncludingCurrent {
		return c.replyThenClose(wire.MsgSessionRevoked, e.RequestID, done)
	}
	return c.reply(wire.MsgSessionRevoked, e.RequestID, done)
}

func auditScope(includingCurrent bool) string {
	if includingCurrent {
		return "all"
	}
	return "all-but-current"
}

// handleAccountDelete erases the caller's account.
//
// The password is re-checked even though the socket is already authenticated: a
// session token lives on the device, so without it anyone holding an unlocked
// phone could destroy the account behind it with two taps. This is the same
// reasoning that gates the destructive half of every other product's settings
// screen, and the reason the body carries a password at all.
func (c *conn) handleAccountDelete(ctx context.Context, e wire.Envelope) error {
	var body wire.AccountDeleteBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad account delete")
	}
	if body.Password == "" {
		return c.replyError(e.RequestID, wire.ErrBadArg, "password required")
	}

	// Charged to the user, not the socket: this runs an argon2id verify, which is
	// deliberately expensive, and an unmetered one is a CPU-flood primitive that
	// a second connection would otherwise buy a fresh budget for.
	if !c.allowUser(ctx, "account") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many attempts", 5000)
	}

	if err := c.gw.svc.Auth.DeleteAccount(ctx, c.userID, body.Password); err != nil {
		if errors.Is(err, auth.ErrWrongPassword) {
			c.gw.audit(ctx, "account.delete.denied", c.userID, "", "wrong password")
			return c.replyError(e.RequestID, wire.ErrForbidden, "password does not match")
		}
		return c.replyForError(e.RequestID, err)
	}

	// Audited BEFORE the confirmation goes out, because the connection is about to
	// be torn down and an audit sink that writes asynchronously would otherwise
	// race the close. The record names the account that is gone; the optional
	// reason lives here and nowhere else, in particular never on a row that
	// outlives the account.
	c.gw.audit(ctx, "account.delete", c.userID, "", body.Reason)

	return c.replyThenClose(wire.MsgAccountDeleted, e.RequestID,
		wire.AccountDeletedBody{UserID: c.userID, DeletedAt: time.Now().UnixMilli()})
}
