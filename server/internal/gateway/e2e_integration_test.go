package gateway_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/pkg/e2e"
	"github.com/synapse-chat/synapse/pkg/wire"
)

// e2eInit is the X3DH bootstrap the initiator sends with its first secret
// message (analogous to a Signal PreKeySignalMessage). Carried opaquely in the
// SecretMsgBody — the server never reads it.
type e2eInit struct {
	IdentityKey  string `json:"ik"` // base64 X25519
	EphemeralKey string `json:"ek"` // base64 X25519
	Ratchet      string `json:"rh"` // base64 ratchet header
}

func b64(b []byte) string   { return base64.StdEncoding.EncodeToString(b) }
func unb64(s string) []byte { b, _ := base64.StdEncoding.DecodeString(s); return b }

// TestE2EExchangeThroughGateway runs a FULL Double Ratchet exchange between two
// devices through the real gateway: Bob publishes keys (MsgKeyPublish), Alice
// fetches the bundle (MsgKeyFetch), runs X3DH + ratchet, encrypts, and relays the
// ciphertext (MsgSecretSend); Bob receives it (MsgSecretRecv), runs X3DH +
// ratchet, and decrypts — proving the crypto works end-to-end via the server,
// which only ever sees ciphertext.
func TestE2EExchangeThroughGateway(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "ealice", "secret123")
	bob := connect(t, addr, "ebob", "secret123")

	// --- Bob generates and publishes his public prekey bundle via the gateway ---
	bobIK, _ := e2e.GenerateKeyPair()
	bobSign, _ := e2e.GenerateSigningKey()
	bobSPK, _ := e2e.GenerateKeyPair()
	bobOPK, _ := e2e.GenerateKeyPair()
	bob.send(t, wire.MsgKeyPublish, 5, wire.KeyPublishBody{
		IdentityKey:     b64(bobIK.PublicBytes()),
		SigningKey:      b64(bobSign.PublicBytes()),
		SignedPreKey:    b64(bobSPK.PublicBytes()),
		SignedPreKeySig: b64(e2e.SignPreKey(bobSign.Priv, bobSPK.PublicBytes())),
		PreKeys:         []string{b64(bobOPK.PublicBytes())},
	})
	// --- Alice fetches Bob's bundle via the gateway ---
	//
	// KeyPublish is fire-and-forget: the protocol gives the publisher no ack, so
	// there is nothing to wait ON. Polling the fetch is therefore the honest
	// synchronization — sleeping a guessed interval passes on an idle machine and
	// fails on a busy one, which is a test that reports load, not correctness.
	var bundle wire.KeyBundleBody
	deadline := time.Now().Add(readTimeout)
	for bundle.IdentityKey == "" && time.Now().Before(deadline) {
		alice.send(t, wire.MsgKeyFetch, 6, wire.KeyFetchBody{UserID: bob.userID, DeviceID: bob.deviceID})
		e, ok := alice.tryRead(t, 250*time.Millisecond)
		if !ok || e.Type != wire.MsgKeyBundle {
			continue // not published yet: the server answers "no keys for device"
		}
		_ = wire.Unmarshal(e.Body, &bundle)
	}
	if bundle.IdentityKey == "" {
		t.Fatal("Bob's prekey bundle never became fetchable")
	}
	if bundle.OneTimePreKey == "" {
		t.Fatal("expected a one-time prekey in the bundle")
	}

	// --- Alice runs X3DH + initializes the ratchet, verifying the signature ---
	aliceIK, _ := e2e.GenerateKeyPair()
	aliceEK, _ := e2e.GenerateKeyPair()
	pkb := e2e.PreKeyBundle{
		IdentityKey:     unb64(bundle.IdentityKey),
		SigningKey:      unb64(bundle.SigningKey),
		SignedPreKey:    unb64(bundle.SignedPreKey),
		SignedPreKeySig: unb64(bundle.SignedPreKeySig),
		OneTimePreKey:   unb64(bundle.OneTimePreKey),
	}
	skA, ephPub, err := e2e.X3DHInitiator(e2e.InitiatorKeys{Identity: aliceIK, Ephemeral: aliceEK}, pkb)
	if err != nil {
		t.Fatalf("X3DH initiator: %v", err)
	}
	aliceSess, err := e2e.NewInitiatorSession(skA, bobSPK.PublicBytes())
	if err != nil {
		t.Fatal(err)
	}
	hdr, ct, err := aliceSess.Encrypt([]byte("secret hello"))
	if err != nil {
		t.Fatal(err)
	}

	// --- Alice relays the opaque ciphertext to Bob's device via the gateway ---
	init := e2eInit{IdentityKey: b64(aliceIK.PublicBytes()), EphemeralKey: b64(ephPub), Ratchet: b64(e2e.MarshalHeader(hdr))}
	initJSON, _ := json.Marshal(init)
	alice.send(t, wire.MsgSecretSend, 7, wire.SecretMsgBody{
		ToUserID: bob.userID, ToDeviceID: bob.deviceID,
		RatchetHeader: string(initJSON), Ciphertext: b64(ct),
	})

	// --- Bob receives, runs X3DH responder + ratchet, and decrypts ---
	recv := bob.readUntil(t, wire.MsgSecretRecv)
	var sm wire.SecretMsgBody
	_ = wire.Unmarshal(recv.Body, &sm)
	if sm.FromUserID != alice.userID {
		t.Fatalf("sender not stamped: %+v", sm)
	}
	var gotInit e2eInit
	if err := json.Unmarshal([]byte(sm.RatchetHeader), &gotInit); err != nil {
		t.Fatal(err)
	}
	skB, err := e2e.X3DHResponder(
		e2e.ResponderKeys{Identity: bobIK, SignedPreKey: bobSPK, OneTimePreKey: bobOPK},
		unb64(gotInit.IdentityKey), unb64(gotInit.EphemeralKey), true)
	if err != nil {
		t.Fatalf("X3DH responder: %v", err)
	}
	bobSess, err := e2e.NewResponderSession(skB, bobSPK)
	if err != nil {
		t.Fatal(err)
	}
	gotHdr, err := e2e.UnmarshalHeader(unb64(gotInit.Ratchet))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := bobSess.Decrypt(gotHdr, unb64(sm.Ciphertext))
	if err != nil {
		t.Fatalf("bob decrypt: %v", err)
	}
	if string(plain) != "secret hello" {
		t.Fatalf("decrypted %q, want 'secret hello'", plain)
	}
}
