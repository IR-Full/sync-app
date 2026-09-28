package e2e

import "errors"

var (
	errNoOneTimeKey = errors.New("e2e: responder missing one-time prekey")
	// ErrDecrypt is returned when authentication/decryption fails (tampering,
	// wrong key, or a replay of an already-consumed message key).
	ErrDecrypt = errors.New("e2e: decryption failed")
	// maxSkip bounds how many missing messages we will derive keys for in ONE
	// call, to stop a malicious header from forcing unbounded work.
	maxSkip = 1000
	// maxSkippedKeys bounds how many skipped message keys a session RETAINS.
	//
	// maxSkip alone was never a bound on memory: every DH ratchet step restarts
	// the per-call count, so a peer — or anyone who can reach the relay, which is
	// any account — could keep adding keys to a map that never shrank. The web
	// port persists this map to storage, so the growth outlived the process too.
	// Two chains' worth is generous against real out-of-order delivery, which
	// runs to a handful of messages.
	maxSkippedKeys = 2 * maxSkip
)
