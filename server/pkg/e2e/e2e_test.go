package e2e

import (
	"bytes"
	"errors"
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
	if _, _, err := X3DHInitiator(InitiatorKeys{Identity: aliceIK, Ephemeral: aliceEK}, bundle); !errors.Is(err, ErrBadPreKeySignature) {
		t.Fatalf("expected ErrBadPreKeySignature, got %v", err)
	}
}

// A hostile directory does not have to forge a signature — the cheaper attack is
// to send no signature at all. Verification used to be conditional on the bundle
// carrying one, so omitting both fields skipped the check entirely and X3DH ran
// against whatever prekey the directory chose. Each field is dropped
// independently as well, because a half-filled bundle must not be a third path.
func TestX3DHRejectsBundleWithNoSignature(t *testing.T) {
	bobIK, _ := GenerateKeyPair()
	bobSign, _ := GenerateSigningKey()
	attacker, _ := GenerateKeyPair()

	signed := func(b PreKeyBundle) PreKeyBundle {
		b.SignedPreKeySig = SignPreKey(bobSign.Priv, b.SignedPreKey)
		return b
	}
	base := PreKeyBundle{
		IdentityKey:  bobIK.PublicBytes(),
		SigningKey:   bobSign.PublicBytes(),
		SignedPreKey: attacker.PublicBytes(), // the key the directory wants used
	}

	cases := map[string]PreKeyBundle{
		"no signing key and no signature": base,
		"signature dropped":               func() PreKeyBundle { b := signed(base); b.SignedPreKeySig = nil; return b }(),
		"signing key dropped":             func() PreKeyBundle { b := signed(base); b.SigningKey = nil; return b }(),
	}

	aliceIK, _ := GenerateKeyPair()
	aliceEK, _ := GenerateKeyPair()
	for name, bundle := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := X3DHInitiator(InitiatorKeys{Identity: aliceIK, Ephemeral: aliceEK}, bundle)
			if !errors.Is(err, ErrBadPreKeySignature) {
				t.Fatalf("an unsigned bundle must be rejected; got err=%v", err)
			}
		})
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

// --------------------------------------------- header as additional data
//
// The header is authenticated, not encrypted, and it crosses the wire as its own
// bytes. These three pin down what "authenticated" means here: the AD is the
// bytes that travelled, not a re-encoding of the values parsed out of them.

func TestTheHeaderOnTheWireIsTheBytesThatWereAuthenticated(t *testing.T) {
	// If these two ever differ, the receiver is verifying against something the
	// sender never signed, and every peer agrees only by coincidence of having
	// the same JSON encoder.
	alice, _ := setupSessions(t)
	hdr, _, err := alice.Encrypt([]byte("m"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(MarshalHeader(hdr), hdr.bytes()) {
		t.Fatalf("wire form %q != authenticated form %q", MarshalHeader(hdr), hdr.bytes())
	}
}

func TestADecodedHeaderAuthenticatesAgainstItsOriginalBytes(t *testing.T) {
	// A peer that serializes differently — another field order, whitespace,
	// anything a fifth implementation might do — must still interoperate, because
	// its own bytes are what gets checked. Re-marshalling here would reject this
	// message with ErrDecrypt, which reads exactly like a forgery and names
	// nothing an operator could act on.
	alice, bob := setupSessions(t)
	hdr, ct, err := alice.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	onWire := MarshalHeader(hdr)
	parsed, err := UnmarshalHeader(onWire)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := bob.Decrypt(parsed, ct)
	if err != nil {
		t.Fatalf("a header that round-tripped through the wire did not open: %v", err)
	}
	if string(pt) != "hello" {
		t.Fatalf("got %q", pt)
	}
}

func TestUnmarshalHeaderKeepsANonCanonicalEncodingVerbatim(t *testing.T) {
	// The interop property itself, at the level it lives at. A peer emitting the
	// same three values in a different order with whitespace is still a valid
	// sender; what it signed is this byte string, so this byte string is what has
	// to come back out — not what Go's json.Marshal would have written.
	foreign := []byte(`{ "n": 3, "pn": 1, "dh": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" }`)

	h, err := UnmarshalHeader(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if h.N != 3 || h.PN != 1 {
		t.Fatalf("values were not parsed: n=%d pn=%d", h.N, h.PN)
	}
	if !bytes.Equal(h.bytes(), foreign) {
		t.Fatalf("additional data was re-encoded as %q; the peer signed %q", h.bytes(), foreign)
	}
}

func TestATamperedHeaderIsStillRejected(t *testing.T) {
	// Carrying the bytes must not weaken the check it feeds: the counters and the
	// ratchet key are covered because they are inside the bytes being verified.
	alice, bob := setupSessions(t)
	hdr, ct, err := alice.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	forged := bytes.Replace(MarshalHeader(hdr), []byte(`"n":0`), []byte(`"n":7`), 1)
	if bytes.Equal(forged, MarshalHeader(hdr)) {
		t.Fatal("the test did not actually change the header")
	}
	parsed, err := UnmarshalHeader(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Decrypt(parsed, ct); err == nil {
		t.Fatal("a header edited in flight was accepted")
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

// TestForgedFrameDoesNotDamageSession is the regression test for the defect this
// package shipped with: Decrypt used to run the DH ratchet and the skipped-key
// walk BEFORE authenticating, so anyone able to reach the relay could rewrite a
// session's state with one frame and permanently break the real conversation.
//
// The forgery is the realistic one: a header carrying an attacker-generated
// ratchet key, which forces the most state change per frame.
func TestForgedFrameDoesNotDamageSession(t *testing.T) {
	alice, bob := setupSessions(t)

	// Establish the session properly first, so there is something to damage.
	h1, ct1, err := alice.Encrypt([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Decrypt(h1, ct1); err != nil {
		t.Fatal(err)
	}

	// An unrelated party forges a frame at Bob: a fresh ratchet key it controls,
	// a large gap, and ciphertext that cannot authenticate.
	attacker, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	forged := Header{DH: attacker.PublicBytes(), PN: 500, N: 500}
	if _, err := bob.Decrypt(forged, []byte("not a real ciphertext")); err == nil {
		t.Fatal("a forged frame must not decrypt")
	}

	// Bob must still be able to talk to Alice. Before the fix this failed:
	// bob's rk/ckr/dhr had been replaced by the attacker's.
	h2, ct2, err := alice.Encrypt([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := bob.Decrypt(h2, ct2)
	if err != nil {
		t.Fatalf("the session was damaged by a forged frame: %v", err)
	}
	if string(pt) != "second" {
		t.Fatalf("decrypted %q, want 'second'", pt)
	}
}

// A forged frame must also not be able to grow the skipped-key store, which is
// the memory half of the same defect.
func TestForgedFrameStoresNoSkippedKeys(t *testing.T) {
	alice, bob := setupSessions(t)
	h1, ct1, _ := alice.Encrypt([]byte("first"))
	if _, err := bob.Decrypt(h1, ct1); err != nil {
		t.Fatal(err)
	}
	before := len(bob.skipped)

	for i := 0; i < 20; i++ {
		attacker, _ := GenerateKeyPair()
		_, _ = bob.Decrypt(Header{DH: attacker.PublicBytes(), PN: uint32(maxSkip - 1), N: uint32(maxSkip - 1)}, []byte("junk"))
	}
	if got := len(bob.skipped); got != before {
		t.Fatalf("forged frames left %d skipped keys behind (was %d)", got, before)
	}
}

// The retained-key store must stay bounded even under legitimate use, because
// maxSkip only ever bounded a single call.
func TestSkippedKeyStoreIsBounded(t *testing.T) {
	alice, bob := setupSessions(t)

	// Alice sends far more than the cap; Bob receives only the last one each
	// round, so every earlier message becomes a skipped key.
	for round := 0; round < 4; round++ {
		var lastHdr Header
		var lastCT []byte
		for i := 0; i < maxSkip-1; i++ {
			h, ct, err := alice.Encrypt([]byte("filler"))
			if err != nil {
				t.Fatal(err)
			}
			lastHdr, lastCT = h, ct
		}
		if _, err := bob.Decrypt(lastHdr, lastCT); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	if len(bob.skipped) > maxSkippedKeys {
		t.Fatalf("skipped store grew to %d, cap is %d", len(bob.skipped), maxSkippedKeys)
	}
}

// Out-of-order delivery must keep working — the staging must not have cost the
// feature it protects.
func TestOutOfOrderStillDecryptsAfterStaging(t *testing.T) {
	alice, bob := setupSessions(t)

	h1, ct1, _ := alice.Encrypt([]byte("one"))
	h2, ct2, _ := alice.Encrypt([]byte("two"))
	h3, ct3, _ := alice.Encrypt([]byte("three"))

	// Deliver 3, then 1, then 2.
	if pt, err := bob.Decrypt(h3, ct3); err != nil || string(pt) != "three" {
		t.Fatalf("third: %q %v", pt, err)
	}
	if pt, err := bob.Decrypt(h1, ct1); err != nil || string(pt) != "one" {
		t.Fatalf("first: %q %v", pt, err)
	}
	if pt, err := bob.Decrypt(h2, ct2); err != nil || string(pt) != "two" {
		t.Fatalf("second: %q %v", pt, err)
	}
}
