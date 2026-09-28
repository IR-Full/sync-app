package wire

import (
	"bytes"
	"encoding/base64"
	"testing"
)

/*
The two payload encodings, and the asymmetry inside the legacy one.

The legacy form encodes its two halves DIFFERENTLY, and the field comment used to
claim they were both base64. Believing that rejects every real message, because
clients put a JSON object in ratchet_header and JSON-parse it back — which the
integration suite caught the first time this was written the other way. These tests
pin the actual contract so the claim cannot drift again.
*/

func TestSecretPayloadReadsTheBinaryForm(t *testing.T) {
	header := []byte(`{"n":1}`)
	cipher := []byte{0x00, 0xff, 0x7f, 0x80}

	var b SecretMsgBody
	SetSecretPayloadBinary(&b, header, cipher)

	// Setting one form CLEARS the other. Leaving both populated would send the
	// payload twice, turning a change made to save 33% into one that costs 100%.
	if b.RatchetHeader != "" || b.Ciphertext != "" {
		t.Fatalf("the legacy fields were left populated: %+v", b)
	}

	gotH, gotC, ok := SecretPayload(b)
	if !ok {
		t.Fatal("a binary payload did not read back")
	}
	if !bytes.Equal(gotH, header) || !bytes.Equal(gotC, cipher) {
		t.Fatalf("binary round-trip lost data: %q / %q", gotH, gotC)
	}
}

// TestSecretPayloadReadsTheLegacyForm is the one that matters: the header is TEXT
// and the ciphertext is base64, and confusing them breaks every message.
func TestSecretPayloadReadsTheLegacyForm(t *testing.T) {
	// A JSON header, exactly as the clients send it — and NOT valid base64, which
	// is the whole point.
	header := []byte(`{"ik":"abc","ek":"def","rh":"ghi"}`)
	cipher := []byte{0x00, 0x01, 0xfe, 0xff}

	var b SecretMsgBody
	SetSecretPayloadLegacy(&b, header, cipher)

	if b.RatchetHeader != string(header) {
		t.Fatalf("the header was re-encoded; it is text: %q", b.RatchetHeader)
	}
	if b.Ciphertext != base64.StdEncoding.EncodeToString(cipher) {
		t.Fatalf("the ciphertext was not base64-encoded: %q", b.Ciphertext)
	}
	if len(b.Header) != 0 || len(b.Cipher) != 0 {
		t.Fatalf("the binary fields were left populated: %+v", b)
	}

	gotH, gotC, ok := SecretPayload(b)
	if !ok {
		t.Fatal("a legacy payload did not read back")
	}
	if !bytes.Equal(gotH, header) || !bytes.Equal(gotC, cipher) {
		t.Fatalf("legacy round-trip lost data: %q / %q", gotH, gotC)
	}
}

// TestSecretPayloadAcceptsAJSONHeaderThatIsNotBase64 is the regression guard for
// the mistake itself. Every real first message carries an X3DH bootstrap as JSON
// in this field.
func TestSecretPayloadAcceptsAJSONHeaderThatIsNotBase64(t *testing.T) {
	b := SecretMsgBody{
		// Braces, quotes and colons are all outside the base64 alphabet.
		RatchetHeader: `{"ik":"AAAA","ek":"BBBB"}`,
		Ciphertext:    base64.StdEncoding.EncodeToString([]byte("ct")),
	}
	gotH, gotC, ok := SecretPayload(b)
	if !ok {
		t.Fatal("a JSON header was rejected; this is the shape of every first message")
	}
	if string(gotH) != b.RatchetHeader {
		t.Fatalf("header = %q, want it verbatim", gotH)
	}
	if string(gotC) != "ct" {
		t.Fatalf("cipher = %q", gotC)
	}
}

// TestSecretPayloadRejectsAnUndecodableCiphertext: a malformed message is refused
// at the boundary rather than stored and relayed, so a peer cannot use the offline
// queue to hold bytes nobody can ever open.
func TestSecretPayloadRejectsAnUndecodableCiphertext(t *testing.T) {
	b := SecretMsgBody{RatchetHeader: `{"n":1}`, Ciphertext: "not!valid!base64!"}
	if _, _, ok := SecretPayload(b); ok {
		t.Fatal("an undecodable ciphertext was accepted")
	}
}

func TestSecretPayloadRejectsAnEmptyBody(t *testing.T) {
	if _, _, ok := SecretPayload(SecretMsgBody{}); ok {
		t.Fatal("an empty body reported a payload")
	}
}

// TestSecretPayloadPrefersBinaryWhenBothAreSet. A peer that sends both is either
// confused or trying to make two payloads look like one message; the binary form is
// what a current client means.
func TestSecretPayloadPrefersBinaryWhenBothAreSet(t *testing.T) {
	b := SecretMsgBody{
		RatchetHeader: "legacy-header",
		Ciphertext:    base64.StdEncoding.EncodeToString([]byte("legacy")),
		Header:        []byte("binary-header"),
		Cipher:        []byte("binary"),
	}
	gotH, gotC, ok := SecretPayload(b)
	if !ok {
		t.Fatal("not ok")
	}
	if string(gotH) != "binary-header" || string(gotC) != "binary" {
		t.Fatalf("the legacy form won: %q / %q", gotH, gotC)
	}
}

// TestSetSecretPayloadForPicksByCapability pins the discriminator. A peer that did
// not negotiate CapSecretQueue reads only the text fields, and handing it the
// binary ones would deliver a message with no content in it.
func TestSetSecretPayloadForPicksByCapability(t *testing.T) {
	header, cipher := []byte(`{"n":1}`), []byte{0x00, 0xff}

	var modern SecretMsgBody
	SetSecretPayloadFor(&modern, CapSecretQueue|CapResume, header, cipher)
	if len(modern.Header) == 0 || modern.RatchetHeader != "" {
		t.Fatalf("a capable peer was given the legacy form: %+v", modern)
	}

	var legacy SecretMsgBody
	SetSecretPayloadFor(&legacy, CapResume, header, cipher)
	if legacy.RatchetHeader == "" || len(legacy.Header) != 0 {
		t.Fatalf("an old peer was given the binary form: %+v", legacy)
	}
	// Either way the payload survives.
	for name, b := range map[string]SecretMsgBody{"modern": modern, "legacy": legacy} {
		gotH, gotC, ok := SecretPayload(b)
		if !ok || !bytes.Equal(gotH, header) || !bytes.Equal(gotC, cipher) {
			t.Fatalf("%s: payload lost (%q/%q, ok=%v)", name, gotH, gotC, ok)
		}
	}
}

// TestBinaryFormIsSmallerThanLegacy is the reason the change exists. Not a
// benchmark — the claim that the encoding stopped paying for base64.
func TestBinaryFormIsSmallerThanLegacy(t *testing.T) {
	header := []byte(`{"dh":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","pn":0,"n":7}`)
	cipher := make([]byte, 4096)

	var modern, legacy SecretMsgBody
	SetSecretPayloadBinary(&modern, header, cipher)
	SetSecretPayloadLegacy(&legacy, header, cipher)

	modernSize := len(Marshal(modern))
	legacySize := len(Marshal(legacy))
	if modernSize >= legacySize {
		t.Fatalf("binary is %d bytes, legacy %d — the encoding is not saving anything",
			modernSize, legacySize)
	}
	// base64 inflates by 4/3, so the saving on a 4 KiB ciphertext should be
	// close to a third of it. A much smaller gap means something is being sent twice.
	saved := legacySize - modernSize
	if want := len(cipher) / 4; saved < want {
		t.Fatalf("saved only %d bytes on a %d-byte ciphertext, expected at least %d",
			saved, len(cipher), want)
	}
}
