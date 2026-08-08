package e2e

// PreKeyBundle is what a device publishes to the key directory so others can
// start a session with it while it is offline (asynchronous session setup). The
// SignedPreKey is signed by the identity's Ed25519 SigningKey, and initiators
// verify that signature before use — so a malicious directory cannot substitute
// a prekey it controls (MITM defense).
type PreKeyBundle struct {
	IdentityKey     []byte // long-term X25519 identity public key (IK)
	SigningKey      []byte // long-term Ed25519 identity signing public key
	SignedPreKey    []byte // medium-term signed prekey public (SPK)
	SignedPreKeySig []byte // Ed25519 signature over SignedPreKey by SigningKey
	OneTimePreKey   []byte // optional one-time prekey public (OPK), consumed once
}

// InitiatorKeys are the private keys the initiator (Alice) holds during X3DH.
type InitiatorKeys struct {
	Identity  *KeyPair // IK_A
	Ephemeral *KeyPair // EK_A (fresh per session)
}

// ResponderKeys are the private keys the responder (Bob) holds to complete X3DH.
type ResponderKeys struct {
	Identity      *KeyPair // IK_B
	SignedPreKey  *KeyPair // SPK_B
	OneTimePreKey *KeyPair // OPK_B (optional; must match the bundle if present)
}
