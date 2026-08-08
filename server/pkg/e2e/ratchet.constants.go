package e2e

import "errors"

var (
	errNoOneTimeKey = errors.New("e2e: responder missing one-time prekey")
	// ErrDecrypt is returned when authentication/decryption fails (tampering,
	// wrong key, or a replay of an already-consumed message key).
	ErrDecrypt = errors.New("e2e: decryption failed")
	// maxSkip bounds how many missing messages we will derive keys for, to stop a
	// malicious header from forcing unbounded work.
	maxSkip = 1000
)
