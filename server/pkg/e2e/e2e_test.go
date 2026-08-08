package e2e

import (
	"bytes"
	"testing"
)

// setupSessions runs X3DH between Alice (initiator) and Bob (responder) and
// returns their live Double Ratchet sessions.
func setupSessions(t *testing.T) (alice, bob *Session) {
	t.Helper()

	// Bob's long-term + prekeys, with the signed prekey signed by his Ed25519
	// identity signing key.
	bobIK, _ := GenerateKeyPair()
	bobSign, _ := GenerateSigningKey()
	bobSPK, _ := GenerateKeyPair()
	bobOPK, _ := GenerateKeyPair()
	bundle := PreKeyBundle{
		IdentityKey:     bobIK.PublicBytes(),
		SigningKey:      bobSign.PublicBytes(),
		SignedPreKey:    bobSPK.PublicBytes(),
		SignedPreKeySig: SignPreKey(bobSign.Priv, bobSPK.PublicBytes()),
		OneTimePreKey:   bobOPK.PublicBytes(),
	}

	// Alice's identity + ephemeral.
	aliceIK, _ := GenerateKeyPair()
	aliceEK, _ := GenerateKeyPair()

	skA, ephPub, err := X3DHInitiator(InitiatorKeys{Identity: aliceIK, Ephemeral: aliceEK}, bundle)
	if err != nil {
		t.Fatal(err)
	}
	skB, err := X3DHResponder(
		ResponderKeys{Identity: bobIK, SignedPreKey: bobSPK, OneTimePreKey: bobOPK},
		aliceIK.PublicBytes(), ephPub, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(skA, skB) {
		t.Fatalf("X3DH shared secrets differ:\n a=%x\n b=%x", skA, skB)
	}

	alice, err = NewInitiatorSession(skA, bobSPK.PublicBytes())
	if err != nil {
		t.Fatal(err)
	}
	bob, err = NewResponderSession(skB, bobSPK)
	if err != nil {
		t.Fatal(err)
	}
	return alice, bob
}

func TestX3DHRejectsForgedPreKey(t *testing.T) {
	// A malicious directory swaps in an attacker-controlled signed prekey but
	// cannot forge the identity signature — the initiator must reject the bundle.
	bobIK, _ := GenerateKeyPair()
	bobSign, _ := GenerateSigningKey()
	bobSPK, _ := GenerateKeyPair()
	attacker, _ := GenerateKeyPair()

	bundle := PreKeyBundle{
		IdentityKey:     bobIK.PublicBytes(),
		SigningKey:      bobSign.PublicBytes(),
		SignedPreKey:    attacker.PublicBytes(),                         // substituted key
		SignedPreKeySig: SignPreKey(bobSign.Priv, bobSPK.PublicBytes()), // sig over the REAL key
	}
	aliceIK, _ := GenerateKeyPair()
	aliceEK, _ := GenerateKeyPair()
	if _, _, err := X3DHInitiator(InitiatorKeys{Identity: aliceIK, Ephemeral: aliceEK}, bundle); err != ErrBadPreKeySignature {
		t.Fatalf("expected ErrBadPreKeySignature, got %v", err)
	}
}

func TestRatchetRoundTrip(t *testing.T) {
	alice, bob := setupSessions(t)

	// Alice → Bob
	h, ct, err := alice.Encrypt([]byte("hello bob"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := bob.Decrypt(h, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "hello bob" {
		t.Fatalf("got %q", pt)
	}

	// Bob → Alice (exercises the reverse ratchet)
	h2, ct2, err := bob.Encrypt([]byte("hi alice"))
	if err != nil {
		t.Fatal(err)
	}
	pt2, err := alice.Decrypt(h2, ct2)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt2) != "hi alice" {
		t.Fatalf("got %q", pt2)
	}
}

func TestRatchetManyMessagesBothWays(t *testing.T) {
	alice, bob := setupSessions(t)
	for i := 0; i < 20; i++ {
		h, ct, _ := alice.Encrypt([]byte("a->b"))
		if pt, err := bob.Decrypt(h, ct); err != nil || string(pt) != "a->b" {
			t.Fatalf("msg %d a->b: %v %q", i, err, pt)
		}
		h2, ct2, _ := bob.Encrypt([]byte("b->a"))
		if pt, err := alice.Decrypt(h2, ct2); err != nil || string(pt) != "b->a" {
			t.Fatalf("msg %d b->a: %v %q", i, err, pt)
		}
	}
}

func TestRatchetOutOfOrder(t *testing.T) {
	alice, bob := setupSessions(t)

	// Alice sends three messages; Bob receives them 1, 3, 2 (skipped-key path).
	h1, c1, _ := alice.Encrypt([]byte("m1"))
	h2, c2, _ := alice.Encrypt([]byte("m2"))
	h3, c3, _ := alice.Encrypt([]byte("m3"))

	if pt, err := bob.Decrypt(h1, c1); err != nil || string(pt) != "m1" {
		t.Fatalf("m1: %v %q", err, pt)
	}
	if pt, err := bob.Decrypt(h3, c3); err != nil || string(pt) != "m3" {
		t.Fatalf("m3: %v %q", err, pt)
	}
	if pt, err := bob.Decrypt(h2, c2); err != nil || string(pt) != "m2" {
		t.Fatalf("m2 (skipped): %v %q", err, pt)
	}
}

func TestRatchetTamperRejected(t *testing.T) {
	alice, bob := setupSessions(t)
	h, ct, _ := alice.Encrypt([]byte("secret"))
	ct[0] ^= 0xFF // flip a bit
	if _, err := bob.Decrypt(h, ct); err == nil {
		t.Fatal("tampered ciphertext must not decrypt")
	}
}

func TestRatchetForwardSecrecyKeysDiffer(t *testing.T) {
	alice, bob := setupSessions(t)
	h1, c1, _ := alice.Encrypt([]byte("same"))
	h2, c2, _ := alice.Encrypt([]byte("same"))
	// Identical plaintext must produce different ciphertext (unique message keys).
	if bytes.Equal(c1, c2) {
		t.Fatal("ciphertexts should differ per message")
	}
	_, _ = bob.Decrypt(h1, c1)
	_, _ = bob.Decrypt(h2, c2)
}
