package e2e

import (
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

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

// X3DHInitiator derives the shared secret from Alice's side. bundle is Bob's
// published prekey bundle. It returns SK plus Alice's ephemeral public key,
// which Bob needs to derive the same SK.
func X3DHInitiator(a InitiatorKeys, bundle PreKeyBundle) (sk []byte, ephemeralPub []byte, err error) {
	// Verify the signed prekey really came from the advertised identity before
	// trusting any of the bundle. This is the MITM defense against a hostile key
	// directory, and it is UNCONDITIONAL.
	//
	// Checking only when the bundle carries a signature would make the defense
	// opt-in for the attacker: a hostile directory need not forge anything, only
	// omit SigningKey and SignedPreKeySig. A missing signature is not a bundle
	// from an older client to be tolerated — it is the exact shape of the attack,
	// and the two are indistinguishable from here.
	if !VerifyPreKey(bundle.SigningKey, bundle.SignedPreKey, bundle.SignedPreKeySig) {
		return nil, nil, ErrBadPreKeySignature
	}
	ikB, err := PublicKeyFromBytes(bundle.IdentityKey)
	if err != nil {
		return nil, nil, err
	}
	spkB, err := PublicKeyFromBytes(bundle.SignedPreKey)
	if err != nil {
		return nil, nil, err
	}

	// DH1 = DH(IK_A, SPK_B); DH2 = DH(EK_A, IK_B); DH3 = DH(EK_A, SPK_B)
	dh1, err := dh(a.Identity.Priv, spkB)
	if err != nil {
		return nil, nil, err
	}
	dh2, err := dh(a.Ephemeral.Priv, ikB)
	if err != nil {
		return nil, nil, err
	}
	dh3, err := dh(a.Ephemeral.Priv, spkB)
	if err != nil {
		return nil, nil, err
	}
	concat := append(append(append([]byte{}, dh1...), dh2...), dh3...)

	// DH4 = DH(EK_A, OPK_B) when a one-time prekey is present.
	if len(bundle.OneTimePreKey) > 0 {
		opkB, err := PublicKeyFromBytes(bundle.OneTimePreKey)
		if err != nil {
			return nil, nil, err
		}
		dh4, err := dh(a.Ephemeral.Priv, opkB)
		if err != nil {
			return nil, nil, err
		}
		concat = append(concat, dh4...)
	}

	return kdfRootFromX3DH(concat), a.Ephemeral.PublicBytes(), nil
}

// X3DHResponder derives the same shared secret from Bob's side, given Alice's
// identity and ephemeral public keys and whether a one-time prekey was used.
func X3DHResponder(b ResponderKeys, aIdentityPub, aEphemeralPub []byte, usedOneTime bool) (sk []byte, err error) {
	ikA, err := PublicKeyFromBytes(aIdentityPub)
	if err != nil {
		return nil, err
	}
	ekA, err := PublicKeyFromBytes(aEphemeralPub)
	if err != nil {
		return nil, err
	}

	// Mirror of the initiator: DH1 = DH(SPK_B, IK_A); DH2 = DH(IK_B, EK_A);
	// DH3 = DH(SPK_B, EK_A).
	dh1, err := dh(b.SignedPreKey.Priv, ikA)
	if err != nil {
		return nil, err
	}
	dh2, err := dh(b.Identity.Priv, ekA)
	if err != nil {
		return nil, err
	}
	dh3, err := dh(b.SignedPreKey.Priv, ekA)
	if err != nil {
		return nil, err
	}
	concat := append(append(append([]byte{}, dh1...), dh2...), dh3...)

	if usedOneTime {
		if b.OneTimePreKey == nil {
			return nil, errNoOneTimeKey
		}
		dh4, err := dh(b.OneTimePreKey.Priv, ekA)
		if err != nil {
			return nil, err
		}
		concat = append(concat, dh4...)
	}
	return kdfRootFromX3DH(concat), nil
}

// kdfRootFromX3DH turns the concatenated DH outputs into a 32-byte root key.
func kdfRootFromX3DH(dhConcat []byte) []byte {
	// A 32-byte 0xFF prefix is the X3DH domain separator recommended by the spec.
	prefix := make([]byte, 32)
	for i := range prefix {
		prefix[i] = 0xFF
	}
	ikm := append(prefix, dhConcat...)
	out := make([]byte, 32)
	r := hkdf.New(sha256.New, ikm, nil, []byte("SyncApp-X3DH"))
	_, _ = io.ReadFull(r, out)
	return out
}
