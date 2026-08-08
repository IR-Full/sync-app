package e2e

import "errors"

// ErrBadPreKeySignature means a prekey bundle's signed prekey was not validly
// signed by the advertised identity signing key — the bundle must be rejected,
// as it may have been substituted by a malicious key directory (MITM).
var ErrBadPreKeySignature = errors.New("e2e: bad signed-prekey signature")
