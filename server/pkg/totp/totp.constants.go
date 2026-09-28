package totp

import "time"

const (
	// digits is the code length. Six, because that is what every authenticator
	// app shows and what users expect to type; eight is permitted by RFC 6238 and
	// supported by almost nothing.
	digits = 6
	// period is one time step. Thirty seconds is the RFC default and, again, what
	// apps assume for a URI that does not say otherwise.
	period = 30 * time.Second
	// secretBytes is the shared-secret length. Twenty bytes matches SHA-1's output
	// size, which is the size RFC 4226 recommends and every app handles.
	secretBytes = 20
)
