package e2e

import (
	"errors"
	"testing"
)

// TestPinningCatchesASwappedIdentityKey pins the property that makes TOFU worth
// having: the key directory is the one component a compromised server fully
// controls, and without a pin a swapped key is indistinguishable from a normal
// session. Detection must not depend on a human remembering to compare a safety
// number.
func TestPinningCatchesASwappedIdentityKey(t *testing.T) {
	honestID, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	honestSign, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	attackerID, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	attackerSign, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	honest := struct{ IdentityPub, SigningPub []byte }{honestID.PublicBytes(), honestSign.PublicBytes()}
	attacker := struct{ IdentityPub, SigningPub []byte }{attackerID.PublicBytes(), attackerSign.PublicBytes()}

	ts := NewTrustStore()

	// First contact is trusted — and flagged as first use, so a client can say so.
	firstUse, err := ts.Verify("u1", "d1", honest.IdentityPub, honest.SigningPub)
	if err != nil {
		t.Fatalf("first sighting must not error: %v", err)
	}
	if !firstUse {
		t.Fatal("first sighting was not reported as first use")
	}
	ts.Accept("u1", "d1", honest.IdentityPub, honest.SigningPub)

	// The same device, same keys: silent success, which is the common case.
	firstUse, err = ts.Verify("u1", "d1", honest.IdentityPub, honest.SigningPub)
	if err != nil || firstUse {
		t.Fatalf("a known device was not recognized: firstUse=%v err=%v", firstUse, err)
	}

	// The directory now hands out someone else's key for that device.
	if _, err := ts.Verify("u1", "d1", attacker.IdentityPub, attacker.SigningPub); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("a swapped identity key was accepted (err=%v)", err)
	}

	// A different DEVICE of the same user is a separate pin, not a violation —
	// otherwise adding a phone would look like an attack.
	if firstUse, err := ts.Verify("u1", "d2", attacker.IdentityPub, attacker.SigningPub); err != nil || !firstUse {
		t.Fatalf("a new device should be first-use, got firstUse=%v err=%v", firstUse, err)
	}

	// After the human confirms a genuine reinstall, the new key becomes the pin.
	ts.Accept("u1", "d1", attacker.IdentityPub, attacker.SigningPub)
	if _, err := ts.Verify("u1", "d1", attacker.IdentityPub, attacker.SigningPub); err != nil {
		t.Fatalf("an accepted key was not trusted afterwards: %v", err)
	}
	if _, err := ts.Verify("u1", "d1", honest.IdentityPub, honest.SigningPub); !errors.Is(err, ErrIdentityChanged) {
		t.Fatal("the replaced key is still trusted")
	}
}
