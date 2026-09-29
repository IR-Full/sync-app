package gateway

import (
	"context"
	"errors"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

/*
Account security: changing a password, and the second factor.

The two halves of one gap. Without a password change a leaked password is
permanent — revoking every session does not stop whoever knows it from signing in
again a minute later. Without a second factor the one credential is interceptable
and is the only one.

Everything here is metered under the same per-user budget as the other expensive
writes, because both paths run argon2id — cheap to ask for, memory-hard to serve.
*/

// handlePasswordChange replaces the caller password and signs out their other
// sessions.
func (c *conn) handlePasswordChange(ctx context.Context, e wire.Envelope) error {
	if !c.allowUser(ctx, "password") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many attempts", 5000)
	}
	var body wire.PasswordChangeBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad password body")
	}
	// The caller session is spared so they are not signed out of the device they
	// are using to secure the account.
	n, err := c.gw.svc.Auth.ChangePassword(ctx, c.userID, body.OldPassword, body.NewPassword, c.sessionID)
	switch {
	case errors.Is(err, auth.ErrWrongPassword):
		// Audited, because a run of these on one account is what a takeover attempt
		// looks like from the inside.
		c.gw.audit(ctx, "user.password.denied", c.userID, "", "old password did not match")
		return c.replyError(e.RequestID, wire.ErrForbidden, "current password is incorrect")
	case errors.Is(err, auth.ErrWeakPassword):
		return c.replyError(e.RequestID, wire.ErrBadArg, err.Error())
	case errors.Is(err, auth.ErrSamePassword):
		return c.replyError(e.RequestID, wire.ErrBadArg, "the new password matches the current one")
	case errors.Is(err, store.ErrUnsupported):
		return c.replyError(e.RequestID, wire.ErrUnsupported, "password changes are not available")
	case err != nil:
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "user.password.changed", c.userID, "", "other sessions revoked")
	return c.reply(wire.MsgPasswordChanged, e.RequestID,
		wire.PasswordChangedBody{SessionsRevoked: int32(n)})
}

// handleTOTPSetup begins enrolment and returns the secret plus its QR URI.
//
// The secret travels to the client in the clear, over the TLS the whole protocol
// rides inside. There is no alternative: the user has to be able to enter it into
// an authenticator, so at some point it is on their screen. What the design does
// guarantee is that it is stored encrypted and never shown again.
func (c *conn) handleTOTPSetup(ctx context.Context, e wire.Envelope) error {
	if !c.allowUser(ctx, "totp") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many attempts", 5000)
	}
	secret, uri, err := c.gw.svc.Auth.BeginTOTP(ctx, c.userID, c.gw.cfg.TOTPIssuer)
	switch {
	case errors.Is(err, auth.ErrTwoFactorEnabled):
		// Refused rather than silently replacing a working factor: a stolen session
		// must not be able to swap the factor for one it controls.
		return c.replyError(e.RequestID, wire.ErrForbidden,
			"two-factor is already enabled; disable it first")
	case errors.Is(err, auth.ErrNoTOTPKey):
		// The operator has not configured the at-rest key, so the secret cannot be
		// stored encrypted — and is therefore not stored at all. Reported as
		// unsupported rather than as a server error, because that is what it is
		// from the client point of view, and the message says what to fix.
		c.log.Error("two-factor enrolment refused: SYNCAPP_TOTP_KEY is not configured")
		return c.replyError(e.RequestID, wire.ErrUnsupported, "two-factor is not configured on this server")
	case errors.Is(err, store.ErrUnsupported):
		return c.replyError(e.RequestID, wire.ErrUnsupported, "two-factor is not available")
	case err != nil:
		return c.replyForError(e.RequestID, err)
	}
	return c.reply(wire.MsgTOTPSetupInfo, e.RequestID, wire.TOTPSetupInfoBody{Secret: secret, URI: uri})
}

// handleTOTPConfirm enrols the factor and returns the recovery codes ONCE.
func (c *conn) handleTOTPConfirm(ctx context.Context, e wire.Envelope) error {
	if !c.allowUser(ctx, "totp") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many attempts", 5000)
	}
	var body wire.TOTPConfirmBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad confirm body")
	}
	codes, err := c.gw.svc.Auth.ConfirmTOTP(ctx, c.userID, body.Code)
	switch {
	case errors.Is(err, auth.ErrBadTOTPCode):
		return c.replyError(e.RequestID, wire.ErrTwoFactorInvalid, "that code did not match")
	case errors.Is(err, auth.ErrTwoFactorEnabled):
		return c.replyError(e.RequestID, wire.ErrForbidden, "two-factor is already enabled")
	case errors.Is(err, store.ErrNotFound):
		// Confirming without having begun. A distinct message, because the client
		// state machine is what is wrong, not the code the user typed.
		return c.replyError(e.RequestID, wire.ErrBadArg, "start setup before confirming")
	case errors.Is(err, store.ErrUnsupported), errors.Is(err, auth.ErrNoTOTPKey):
		return c.replyError(e.RequestID, wire.ErrUnsupported, "two-factor is not available")
	case err != nil:
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "user.totp.enabled", c.userID, "", "")
	// The only time RecoveryCodes is ever populated. They are stored as argon2id
	// hashes, so there is nothing to show later — which is what makes a leak of the
	// table worthless, and why the client must tell the user to save them now.
	return c.reply(wire.MsgTOTPState, e.RequestID, wire.TOTPStateBody{
		Enabled:       true,
		RecoveryLeft:  int32(len(codes)),
		RecoveryCodes: codes,
	})
}

// handleTOTPDisable removes the factor, requiring the password and a live code.
func (c *conn) handleTOTPDisable(ctx context.Context, e wire.Envelope) error {
	if !c.allowUser(ctx, "totp") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "too many attempts", 5000)
	}
	var body wire.TOTPDisableBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad disable body")
	}
	err := c.gw.svc.Auth.DisableTOTP(ctx, c.userID, body.Password, body.Code)
	switch {
	case errors.Is(err, auth.ErrWrongPassword):
		c.gw.audit(ctx, "user.totp.disable.denied", c.userID, "", "wrong password")
		return c.replyError(e.RequestID, wire.ErrForbidden, "current password is incorrect")
	case errors.Is(err, auth.ErrBadTOTPCode):
		c.gw.audit(ctx, "user.totp.disable.denied", c.userID, "", "wrong code")
		return c.replyError(e.RequestID, wire.ErrTwoFactorInvalid, "that code did not match")
	case errors.Is(err, store.ErrNotFound):
		// Nothing enrolled. Reported as success-shaped state rather than an error:
		// the caller asked for the factor to be off, and it is off.
		return c.reply(wire.MsgTOTPState, e.RequestID, wire.TOTPStateBody{})
	case errors.Is(err, store.ErrUnsupported), errors.Is(err, auth.ErrNoTOTPKey):
		return c.replyError(e.RequestID, wire.ErrUnsupported, "two-factor is not available")
	case err != nil:
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "user.totp.disabled", c.userID, "", "")
	return c.reply(wire.MsgTOTPState, e.RequestID, wire.TOTPStateBody{})
}

// totpState answers "is the second factor on, and how many recovery codes are
// left", which is what a settings screen draws.
//
// The remaining count is exposed because losing the last code and the phone
// together is the state there is no way back from, and a client that cannot see
// the count cannot warn before it happens.
func (c *conn) totpState(ctx context.Context) wire.TOTPStateBody {
	if !c.gw.svc.Auth.TwoFactorEnabled(ctx, c.userID) {
		return wire.TOTPStateBody{}
	}
	return wire.TOTPStateBody{
		Enabled:      true,
		RecoveryLeft: int32(c.gw.svc.Auth.RecoveryCodesLeft(ctx, c.userID)),
	}
}

/*
handleTOTPState answers TOTP_STATE as a REQUEST.

The type already existed as a reply — every write path returns it — and nothing
dispatched it as a question, so a settings screen had no way to learn whether the
second factor was on without changing something. The web client worked around that
with a no-op `refresh()` that relied on a write having happened first; a screen
opened on a fresh page load simply drew "off" regardless of the truth, which is the
worst possible default for a security toggle.

Deliberately NOT in the argon2id rate-limit group with its siblings. This one
hashes nothing and writes nothing — metering it would throttle the ordinary act of
opening a settings screen, while the paths that actually cost something stay
metered.
*/
func (c *conn) handleTOTPState(ctx context.Context, e wire.Envelope) error {
	return c.reply(wire.MsgTOTPState, e.RequestID, c.totpState(ctx))
}
