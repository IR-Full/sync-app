package e2e

import "crypto/ecdh"

// Header travels (authenticated but not encrypted) with each ciphertext.
type Header struct {
	DH []byte `json:"dh"` // sender's current ratchet public key
	PN uint32 `json:"pn"` // number of messages in the previous sending chain
	N  uint32 `json:"n"`  // message number in the current sending chain
}

// Session is one Double Ratchet session between two devices. It is NOT safe for
// concurrent use; callers serialize per session.
type Session struct {
	dhs     *KeyPair          // our current ratchet key pair (DHs)
	dhr     *ecdh.PublicKey   // their current ratchet public key (DHr)
	rk      []byte            // root key
	cks     []byte            // sending chain key
	ckr     []byte            // receiving chain key
	ns      uint32            // messages sent in current sending chain
	nr      uint32            // messages received in current receiving chain
	pn      uint32            // length of previous sending chain
	skipped map[string][]byte // (dhPub|N) -> message key, for out-of-order delivery
}
