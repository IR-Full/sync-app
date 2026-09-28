package gateway_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/keydir"
	"github.com/SyncApp-chat/SyncApp/pkg/e2e"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
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

// validKeyPublish mints a well-formed prekey bundle.
//
// The gateway validates shape and signature at publish time, so a test that only
// needs "some device published keys" still has to publish real ones. Generating
// them is three key pairs and a signature — cheaper than the alternative, which
// is a placeholder that drifts away from what the server accepts.
func validKeyPublish(t *testing.T) wire.KeyPublishBody {
	t.Helper()
	ik, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	sign, err := e2e.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	spk, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	opk, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return wire.KeyPublishBody{
		IdentityKey:     b64(ik.PublicBytes()),
		SigningKey:      b64(sign.PublicBytes()),
		SignedPreKey:    b64(spk.PublicBytes()),
		SignedPreKeySig: b64(e2e.SignPreKey(sign.Priv, spk.PublicBytes())),
		PreKeys:         []string{b64(opk.PublicBytes())},
	}
}

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
	// Publishing now ACKs with KEY_STATE, so the test waits on the publish itself
	// rather than polling the fetch until it stops failing. That changes what is
	// actually asserted: the old loop could only conclude "eventually fetchable", and
	// reported a busy machine as a broken directory.
	bob.readUntil(t, wire.MsgKeyState)

	// --- Alice fetches Bob's bundle via the gateway ---
	alice.send(t, wire.MsgKeyFetch, 6, wire.KeyFetchBody{UserID: bob.userID, DeviceID: bob.deviceID})
	var bundle wire.KeyBundleBody
	_ = wire.Unmarshal(alice.readUntil(t, wire.MsgKeyBundle).Body, &bundle)
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

// TestKeyPublishRejectsMalformedBundle asserts the directory refuses bundles it
// cannot serve usefully.
//
// The cases are the two failure shapes that used to pass straight through: a
// field that is not a key at all (so the directory stored arbitrary bytes, with
// no expiry and no ceiling beyond the frame cap), and a field that is the right
// size but whose signature does not verify (so every peer's X3DH would reject a
// bundle the server kept serving).
func TestKeyPublishRejectsMalformedBundle(t *testing.T) {
	addr := startGateway(t)

	cases := map[string]func(wire.KeyPublishBody) wire.KeyPublishBody{
		"identity key is not base64":   func(b wire.KeyPublishBody) wire.KeyPublishBody { b.IdentityKey = "!!!"; return b },
		"identity key is wrong length": func(b wire.KeyPublishBody) wire.KeyPublishBody { b.IdentityKey = b64([]byte("short")); return b },
		"signing key is wrong length":  func(b wire.KeyPublishBody) wire.KeyPublishBody { b.SigningKey = b64(make([]byte, 16)); return b },
		"signature is wrong length":    func(b wire.KeyPublishBody) wire.KeyPublishBody { b.SignedPreKeySig = b64(make([]byte, 8)); return b },
		"signature does not verify":    func(b wire.KeyPublishBody) wire.KeyPublishBody { b.SignedPreKeySig = b64(make([]byte, 64)); return b },
		"prekey is wrong length":       func(b wire.KeyPublishBody) wire.KeyPublishBody { b.PreKeys = []string{b64(make([]byte, 31))}; return b },
	}

	i := 0
	for name, corrupt := range cases {
		i++
		t.Run(name, func(t *testing.T) {
			c := connect(t, addr, "kp"+itoaTest(i), "secret123")
			c.send(t, wire.MsgKeyPublish, 7, corrupt(validKeyPublish(t)))

			e := c.readUntil(t, wire.MsgError)
			var eb wire.ErrorBody
			_ = wire.Unmarshal(e.Body, &eb)
			if eb.Code != wire.ErrBadArg {
				t.Fatalf("want ErrBadArg, got code %d (%q)", eb.Code, eb.Message)
			}
		})
	}
}

// A bundle that passes validation must still reach the directory — the guard is
// meant to reject junk, not to break publishing.
func TestKeyPublishAcceptsValidBundle(t *testing.T) {
	addr := startGateway(t)
	bob := connect(t, addr, "kpok", "secret123")
	bob.send(t, wire.MsgKeyPublish, 7, validKeyPublish(t))
	bob.readUntil(t, wire.MsgKeyState)

	alice := connect(t, addr, "kpokpeer", "secret123")
	alice.send(t, wire.MsgKeyFetch, 8, wire.KeyFetchBody{UserID: bob.userID, DeviceID: bob.deviceID})
	var bundle wire.KeyBundleBody
	_ = wire.Unmarshal(alice.readUntil(t, wire.MsgKeyBundle).Body, &bundle)
	if bundle.IdentityKey == "" {
		t.Fatal("a valid bundle did not reach the directory")
	}
}

// The key directory was the one way to reach a person that a block did not
// cover: resolveChat, PROFILE_GET and SECRET_SEND all refuse, but a blocked user
// could still enumerate their target's devices and pull a fresh prekey bundle
// for each. The keys are public, so the leak is metadata rather than content —
// which devices exist, and when they come and go.
func TestKeyDirectoryHonoursBlocking(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "kdalice", "secret123")
	bob := connect(t, addr, "kdbob", "secret123")

	// KEY_STATE is sent after the directory write, so once it arrives the bundle
	// is fetchable. Waiting for it replaces a fetch-until-visible loop: that loop
	// re-sent KEY_FETCH_ALL without pausing, and on a slow CI runner it spent the
	// whole flood budget before Bob's publish landed — the fetches below were then
	// answered with ErrFlood and the subtests timed out waiting for bundles.
	bob.send(t, wire.MsgKeyPublish, 4, validKeyPublish(t))
	bob.readUntil(t, wire.MsgKeyState)

	// Alice can see Bob's device before the block — otherwise the assertions
	// below would pass on an empty directory rather than on the block.
	var before wire.KeyBundlesBody
	alice.send(t, wire.MsgKeyFetchAll, 5, wire.KeyFetchBody{UserID: bob.userID})
	_ = wire.Unmarshal(alice.readUntil(t, wire.MsgKeyBundles).Body, &before)
	if len(before.Bundles) == 0 {
		t.Fatal("Bob's bundle never became fetchable, so the block proves nothing")
	}

	bob.send(t, wire.MsgBlock, 6, wire.BlockBody{Target: alice.userID, Blocked: true})
	bob.readUntil(t, wire.MsgContactList)

	t.Run("fetch all returns an empty list", func(t *testing.T) {
		// Empty, not an error: that is what an account with no published devices
		// returns, so a blocked caller cannot tell the two apart.
		alice.send(t, wire.MsgKeyFetchAll, 7, wire.KeyFetchBody{UserID: bob.userID})
		e := alice.readUntil(t, wire.MsgKeyBundles)
		var got wire.KeyBundlesBody
		_ = wire.Unmarshal(e.Body, &got)
		if len(got.Bundles) != 0 {
			t.Fatalf("a blocked caller still saw %d device(s)", len(got.Bundles))
		}
	})

	t.Run("fetch one reads as not found", func(t *testing.T) {
		alice.send(t, wire.MsgKeyFetch, 8, wire.KeyFetchBody{UserID: bob.userID, DeviceID: bob.deviceID})
		e := alice.readUntil(t, wire.MsgError)
		var eb wire.ErrorBody
		_ = wire.Unmarshal(e.Body, &eb)
		if eb.Code != wire.ErrNotFound {
			t.Fatalf("want ErrNotFound (same as an unpublished device), got %d", eb.Code)
		}
	})

	t.Run("the block cuts both ways", func(t *testing.T) {
		// Bob blocked Alice, so Bob must not be able to enumerate Alice's devices
		// either — a one-directional block would just relocate the problem.
		// Published and acknowledged first, so an empty answer below is the block
		// and not a race with the publish.
		alice.send(t, wire.MsgKeyPublish, 9, validKeyPublish(t))
		alice.readUntil(t, wire.MsgKeyState)
		bob.send(t, wire.MsgKeyFetchAll, 10, wire.KeyFetchBody{UserID: alice.userID})
		e := bob.readUntil(t, wire.MsgKeyBundles)
		var got wire.KeyBundlesBody
		_ = wire.Unmarshal(e.Body, &got)
		if len(got.Bundles) != 0 {
			t.Fatalf("the blocker still saw %d device(s) of the blocked user", len(got.Bundles))
		}
	})
}

// Multi-device sync depends on a client fetching its OWN other devices, so the
// block check must never apply to itself.
func TestKeyDirectoryAlwaysReturnsOwnDevices(t *testing.T) {
	addr := startGateway(t)
	me := connect(t, addr, "kdself", "secret123")
	me.send(t, wire.MsgKeyPublish, 4, validKeyPublish(t))
	me.readUntil(t, wire.MsgKeyState) // the write has landed (see above)

	var got wire.KeyBundlesBody
	me.send(t, wire.MsgKeyFetchAll, 5, wire.KeyFetchBody{UserID: me.userID})
	_ = wire.Unmarshal(me.readUntil(t, wire.MsgKeyBundles).Body, &got)
	if len(got.Bundles) == 0 {
		t.Fatal("a client must always be able to fetch its own devices")
	}
}

// A malformed user id must read as the client's mistake, not as an internal
// error and not as a lookup that quietly succeeded.
func TestKeyFetchRejectsMalformedTarget(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "kdbadid", "secret123")

	c.send(t, wire.MsgKeyFetchAll, 4, wire.KeyFetchBody{UserID: "not-a-snowflake"})
	e := c.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	if eb.Code != wire.ErrBadArg {
		t.Fatalf("want ErrBadArg, got %d (%q)", eb.Code, eb.Message)
	}
}

// KEY_PUBLISH answers with KEY_STATE, and the counts in it are the only way a
// device can learn its own one-time prekey balance: peers consume those keys, and
// only the owner can refill. A publisher that cannot see the balance runs the batch
// to zero and drops to the weaker three-DH handshake with nobody informed.
func TestKeyPublishReportsDirectoryState(t *testing.T) {
	addr := startGateway(t)
	bob := connect(t, addr, "kpstate", "secret123")

	first := validKeyPublish(t)
	first.PreKeys = manyPreKeys(t, 3)
	bob.send(t, wire.MsgKeyPublish, 11, first)
	var st wire.KeyStateBody
	_ = wire.Unmarshal(bob.readUntil(t, wire.MsgKeyState).Body, &st)
	if st.OneTimePreKeysLeft != 3 || st.Accepted != 3 {
		t.Fatalf("first publish: left=%d accepted=%d, want 3/3", st.OneTimePreKeysLeft, st.Accepted)
	}
	// A brand new signed prekey has no age to report. Zero here and zero for "the
	// directory does not know" are deliberately the same answer: both mean there is
	// nothing for the client to rotate yet.
	if st.SignedPreKeyAgeMs != 0 {
		t.Fatalf("a signed prekey published just now reported an age of %dms", st.SignedPreKeyAgeMs)
	}

	// Prekeys ACCUMULATE, and Accepted counts only this frame's. A client keeps the
	// private halves, so conflating the two would leave it believing its reserve is
	// larger than it is.
	second := first
	second.PreKeys = manyPreKeys(t, 2)
	bob.send(t, wire.MsgKeyPublish, 12, second)
	_ = wire.Unmarshal(bob.readUntil(t, wire.MsgKeyState).Body, &st)
	if st.OneTimePreKeysLeft != 5 || st.Accepted != 2 {
		t.Fatalf("second publish: left=%d accepted=%d, want 5/2", st.OneTimePreKeysLeft, st.Accepted)
	}
}

// Over the per-publish cap the reply must report what SURVIVED, not what was sent.
// This is the case the count exists for: a client told "all 200 stored" holds
// private keys for public ones the directory dropped, and cannot tell which.
func TestKeyPublishReportsCappedAccepted(t *testing.T) {
	addr := startGateway(t)
	bob := connect(t, addr, "kpcap", "secret123")

	body := validKeyPublish(t)
	body.PreKeys = manyPreKeys(t, keydir.MaxPreKeysPerPublish+10)
	bob.send(t, wire.MsgKeyPublish, 13, body)

	var st wire.KeyStateBody
	_ = wire.Unmarshal(bob.readUntil(t, wire.MsgKeyState).Body, &st)
	if st.Accepted != keydir.MaxPreKeysPerPublish {
		t.Fatalf("accepted=%d, want the per-publish cap %d", st.Accepted, keydir.MaxPreKeysPerPublish)
	}
	if st.OneTimePreKeysLeft != keydir.MaxPreKeysPerPublish {
		t.Fatalf("left=%d, want %d", st.OneTimePreKeysLeft, keydir.MaxPreKeysPerPublish)
	}
}

// KEY_FETCH is charged to the ACCOUNT, not to the socket.
//
// Every fetch CONSUMES one of the target's one-time prekeys, so a per-connection
// bucket alone is no limit at all: open a second connection and get a second
// allowance. Draining a device's batch reveals nothing, but it forces every later
// session with that device down to the weaker three-DH handshake until its owner
// next publishes — somebody else's forward secrecy, degraded from an account that
// need only be unblocked.
func TestKeyFetchIsRateLimitedPerUser(t *testing.T) {
	addr := startGateway(t)
	bob := connect(t, addr, "kfvictim", "secret123")
	bob.send(t, wire.MsgKeyPublish, 14, validKeyPublish(t))
	bob.readUntil(t, wire.MsgKeyState)

	// Two connections for ONE account: the point of the test is that they share a
	// budget, which is exactly what the per-connection flood bucket cannot do.
	drain := connect(t, addr, "kfdrain", "secret123")
	same := login(t, addr, "kfdrain", "secret123")

	limited := false
	for i := 0; i < 40 && !limited; i++ {
		c := drain
		if i%2 == 1 {
			c = same
		}
		c.send(t, wire.MsgKeyFetch, uint64(20+i), wire.KeyFetchBody{UserID: bob.userID, DeviceID: bob.deviceID})
		e, ok := c.tryRead(t, time.Second)
		if !ok || e.Type != wire.MsgError {
			continue
		}
		var eb wire.ErrorBody
		_ = wire.Unmarshal(e.Body, &eb)
		if eb.Code == wire.ErrRateLimited {
			limited = true
			if eb.RetryAfterMs <= 0 {
				t.Errorf("a rate-limit error must say when to retry, got %dms", eb.RetryAfterMs)
			}
		}
	}
	if !limited {
		t.Fatal("KEY_FETCH is not rate limited per user: an account can drain a target's prekeys from parallel connections")
	}
}

// manyPreKeys mints n distinct, well-formed one-time prekeys. They have to be real
// X25519 public keys: the gateway validates every one at publish time, so a
// placeholder would test the validator instead of the directory.
func manyPreKeys(t *testing.T, n int) []string {
	t.Helper()
	out := make([]string, n)
	for i := range out {
		kp, err := e2e.GenerateKeyPair()
		if err != nil {
			t.Fatal(err)
		}
		out[i] = b64(kp.PublicBytes())
	}
	return out
}
