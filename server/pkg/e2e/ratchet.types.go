package e2e

import "crypto/ecdh"

// Header travels (authenticated but not encrypted) with each ciphertext.
type Header struct {
	DH []byte `json:"dh"` // sender's current ratchet public key
	PN uint32 `json:"pn"` // number of messages in the previous sending chain
	N  uint32 `json:"n"`  // message number in the current sending chain

	// raw is the exact serialization this header travels as — set by
	// UnmarshalHeader to the bytes that arrived, and by Encrypt to the bytes it
	// authenticated. bytes() prefers it over re-marshalling.
	//
	// This makes the additional data the AEAD checks literally the bytes on the
	// wire, in both directions, instead of a re-encoding that merely ought to
	// match them. Four independent implementations produce this header — Go,
	// TypeScript, Kotlin, Swift — and until now each receiver parsed the JSON and
	// then re-serialized it to rebuild the AD. That works only while all four
	// emit byte-identical canonical JSON: same field order, no whitespace, the
	// same base64 alphabet and padding. Nothing enforces that, and the day one of
	// them diverges — a field gains omitempty, a port pretty-prints, a base64
	// helper drops padding — every message between the two versions fails to
	// decrypt with ErrDecrypt, which is indistinguishable from a forgery and
	// points at nothing. Carrying the bytes removes the requirement rather than
	// documenting it.
	//
	// Unexported, so it is invisible to encoding/json and cannot round-trip into
	// itself.
	raw []byte
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
	// skippedOrder is the insertion order of skipped, used to evict the oldest
	// once maxSkippedKeys is reached. A Go map has no order of its own, and
	// "drop an arbitrary one" would sometimes drop the key that was about to be
	// used. Entries may name a key already consumed; storeSkipped tolerates that.
	skippedOrder []string
}
