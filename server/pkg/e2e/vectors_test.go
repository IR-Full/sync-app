package e2e

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The cross-language vectors. Every port (web, Android, iOS) replays this file
// in its own unit tests and must reproduce it byte for byte: key agreement,
// X3DH, the ratchet headers and ciphertexts of a whole conversation, and safety
// numbers. Encryption is deterministic once the keys are fixed, so a port that
// produces the same ciphertext as Go is one Go can decrypt, and one that
// decrypts Go's is one Go can talk to.
//
// Regenerate with: go test ./pkg/e2e -run TestVectors -update

var update = flag.Bool("update", false, "rewrite testdata/e2e/vectors.json")

const vectorsPath = "../../testdata/e2e/vectors.json"

type vecKeyPair struct {
	Private []byte `json:"private"`
	Public  []byte `json:"public"`
}

type vecSigningKey struct {
	Seed   []byte `json:"seed"`
	Public []byte `json:"public"`
}

type vecX3DH struct {
	AliceIdentity  vecKeyPair    `json:"alice_identity"`
	AliceEphemeral vecKeyPair    `json:"alice_ephemeral"`
	BobIdentity    vecKeyPair    `json:"bob_identity"`
	BobSigning     vecSigningKey `json:"bob_signing"`
	BobSignedPre   vecKeyPair    `json:"bob_signed_prekey"`
	BobOneTimePre  vecKeyPair    `json:"bob_one_time_prekey"`
	// SignedPreKeySig is Ed25519(bob_signing, bob_signed_prekey.public).
	SignedPreKeySig []byte `json:"signed_prekey_signature"`
	// SharedSecret uses the one-time prekey; SharedSecretNoOneTime does not.
	SharedSecret          []byte `json:"shared_secret"`
	SharedSecretNoOneTime []byte `json:"shared_secret_no_one_time"`
}

// vecStep is one event of the conversation, in order.
//
//	send:   From encrypts Plaintext; the port must produce exactly Header and
//	        Ciphertext.
//	recv:   the other party decrypts message ID and must get its plaintext.
//	forged: the other party is handed message ID with one ciphertext byte
//	        flipped; decryption must fail and leave the session usable.
type vecStep struct {
	Op         string `json:"op"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	ID         string `json:"id"`
	Plaintext  string `json:"plaintext,omitempty"`
	Header     string `json:"header,omitempty"`
	Ciphertext []byte `json:"ciphertext,omitempty"`
}

type vecConversation struct {
	// Ratchet key pairs in the order each session draws them. Alice draws one on
	// creation and one per DH ratchet step; Bob starts from his signed prekey.
	AliceRatchetKeys []vecKeyPair `json:"alice_ratchet_keys"`
	BobRatchetKeys   []vecKeyPair `json:"bob_ratchet_keys"`
	Steps            []vecStep    `json:"steps"`
}

type vecSafety struct {
	LocalID       string `json:"local_id"`
	LocalIdentity []byte `json:"local_identity_key"`
	LocalSigning  []byte `json:"local_signing_key"`
	RemoteID      string `json:"remote_id"`
	RemoteIdent   []byte `json:"remote_identity_key"`
	RemoteSigning []byte `json:"remote_signing_key"`
	Number        string `json:"number"`
}

type vectors struct {
	Comment      string          `json:"comment"`
	X3DH         vecX3DH         `json:"x3dh"`
	Conversation vecConversation `json:"conversation"`
	Safety       []vecSafety     `json:"safety"`
}

// derived returns 32 bytes fixed by label, so regenerating the file with the
// same code yields the same keys.
func derived(label string) []byte {
	h := sha256.Sum256([]byte("syncapp/e2e-vectors/" + label))
	return h[:]
}

func derivedKeyPair(t *testing.T, label string) *KeyPair {
	t.Helper()
	kp, err := keyPairFromPrivate(derived(label))
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func toVec(kp *KeyPair) vecKeyPair {
	return vecKeyPair{Private: kp.Priv.Bytes(), Public: kp.PublicBytes()}
}

// script is the conversation the vectors record. It covers in-order delivery,
// skipped and late messages in one chain, a DH ratchet in each direction with
// out-of-order delivery across it, and a forged frame.
var script = []vecStep{
	{Op: "send", From: "alice", ID: "a1", Plaintext: "hello bob"},
	{Op: "send", From: "alice", ID: "a2", Plaintext: "second, delivered late"},
	{Op: "send", From: "alice", ID: "a3", Plaintext: "третье — не ASCII 🔐"},
	{Op: "recv", To: "bob", ID: "a1"},
	{Op: "forged", To: "bob", ID: "a3"},
	{Op: "recv", To: "bob", ID: "a3"},
	{Op: "recv", To: "bob", ID: "a2"},
	{Op: "send", From: "bob", ID: "b1", Plaintext: "hi alice"},
	{Op: "send", From: "bob", ID: "b2", Plaintext: "b2 overtakes b1"},
	{Op: "recv", To: "alice", ID: "b2"},
	{Op: "recv", To: "alice", ID: "b1"},
	{Op: "send", From: "alice", ID: "a4", Plaintext: "new chain from alice"},
	{Op: "send", From: "alice", ID: "a5", Plaintext: ""},
	{Op: "recv", To: "bob", ID: "a5"},
	{Op: "recv", To: "bob", ID: "a4"},
	{Op: "send", From: "bob", ID: "b3", Plaintext: "and back again"},
	{Op: "recv", To: "alice", ID: "b3"},
	{Op: "send", From: "alice", ID: "a6", Plaintext: "last"},
	{Op: "recv", To: "bob", ID: "a6"},
}

// keySource hands out key pairs in order, failing the test when it runs dry.
type keySource struct {
	t    *testing.T
	keys []*KeyPair
	next int
	// mint, when set, creates keys instead of replaying them (generation).
	mint func(i int) *KeyPair
}

func (k *keySource) draw() (*KeyPair, error) {
	if k.mint != nil {
		kp := k.mint(len(k.keys))
		k.keys = append(k.keys, kp)
		k.next++
		return kp, nil
	}
	if k.next >= len(k.keys) {
		return nil, fmt.Errorf("vector key source exhausted after %d keys", len(k.keys))
	}
	kp := k.keys[k.next]
	k.next++
	return kp, nil
}

func TestVectors(t *testing.T) {
	if *update {
		writeVectors(t)
	}
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("%v (regenerate with -update)", err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	replayVectors(t, v)
}

func writeVectors(t *testing.T) {
	t.Helper()
	aIK, aEK := derivedKeyPair(t, "alice-identity"), derivedKeyPair(t, "alice-ephemeral")
	bIK, bSPK, bOPK := derivedKeyPair(t, "bob-identity"), derivedKeyPair(t, "bob-signed-prekey"), derivedKeyPair(t, "bob-one-time-prekey")
	signing := ed25519.NewKeyFromSeed(derived("bob-signing"))
	sig := SignPreKey(signing, bSPK.PublicBytes())

	bundle := PreKeyBundle{
		IdentityKey: bIK.PublicBytes(), SigningKey: signing.Public().(ed25519.PublicKey),
		SignedPreKey: bSPK.PublicBytes(), SignedPreKeySig: sig, OneTimePreKey: bOPK.PublicBytes(),
	}
	sk, _, err := X3DHInitiator(InitiatorKeys{Identity: aIK, Ephemeral: aEK}, bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.OneTimePreKey = nil
	skNoOPK, _, err := X3DHInitiator(InitiatorKeys{Identity: aIK, Ephemeral: aEK}, bundle)
	if err != nil {
		t.Fatal(err)
	}

	aliceKeys := &keySource{t: t, mint: func(i int) *KeyPair { return derivedKeyPair(t, fmt.Sprintf("alice-ratchet-%d", i)) }}
	bobKeys := &keySource{t: t, mint: func(i int) *KeyPair { return derivedKeyPair(t, fmt.Sprintf("bob-ratchet-%d", i)) }}
	alice, err := newInitiatorSession(sk, bSPK.PublicBytes(), aliceKeys.draw)
	if err != nil {
		t.Fatal(err)
	}
	bob := newResponderSession(sk, bSPK, bobKeys.draw)

	steps := runScript(t, script, map[string]*Session{"alice": alice, "bob": bob})

	v := vectors{
		Comment: "Generated by server/pkg/e2e (go test ./pkg/e2e -run TestVectors -update). " +
			"Keys and bytes are standard base64. Every port must reproduce this file exactly.",
		X3DH: vecX3DH{
			AliceIdentity: toVec(aIK), AliceEphemeral: toVec(aEK),
			BobIdentity:     toVec(bIK),
			BobSigning:      vecSigningKey{Seed: signing.Seed(), Public: signing.Public().(ed25519.PublicKey)},
			BobSignedPre:    toVec(bSPK),
			BobOneTimePre:   toVec(bOPK),
			SignedPreKeySig: sig, SharedSecret: sk, SharedSecretNoOneTime: skNoOPK,
		},
		Conversation: vecConversation{Steps: steps},
	}
	for _, kp := range aliceKeys.keys {
		v.Conversation.AliceRatchetKeys = append(v.Conversation.AliceRatchetKeys, toVec(kp))
	}
	for _, kp := range bobKeys.keys {
		v.Conversation.BobRatchetKeys = append(v.Conversation.BobRatchetKeys, toVec(kp))
	}

	aliceSign := ed25519.NewKeyFromSeed(derived("alice-signing"))
	for _, c := range []struct{ localID, remoteID string }{{"1001", "2002"}, {"2002", "1001"}} {
		local, remote := vecSafety{LocalID: c.localID}, vecSafety{}
		if c.localID == "1001" {
			local.LocalIdentity, local.LocalSigning = aIK.PublicBytes(), aliceSign.Public().(ed25519.PublicKey)
			remote.RemoteIdent, remote.RemoteSigning = bIK.PublicBytes(), signing.Public().(ed25519.PublicKey)
		} else {
			local.LocalIdentity, local.LocalSigning = bIK.PublicBytes(), signing.Public().(ed25519.PublicKey)
			remote.RemoteIdent, remote.RemoteSigning = aIK.PublicBytes(), aliceSign.Public().(ed25519.PublicKey)
		}
		s := vecSafety{
			LocalID: c.localID, LocalIdentity: local.LocalIdentity, LocalSigning: local.LocalSigning,
			RemoteID: c.remoteID, RemoteIdent: remote.RemoteIdent, RemoteSigning: remote.RemoteSigning,
		}
		s.Number = SafetyNumber(s.LocalID, s.LocalIdentity, s.LocalSigning, s.RemoteID, s.RemoteIdent, s.RemoteSigning)
		v.Safety = append(v.Safety, s)
	}

	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectorsPath, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runScript plays the conversation and returns the steps with their bytes
// filled in.
func runScript(t *testing.T, steps []vecStep, sessions map[string]*Session) []vecStep {
	t.Helper()
	sent := map[string]vecStep{}
	var out []vecStep
	for _, st := range steps {
		switch st.Op {
		case "send":
			hdr, ct, err := sessions[st.From].Encrypt([]byte(st.Plaintext))
			if err != nil {
				t.Fatalf("%s: %v", st.ID, err)
			}
			st.Header, st.Ciphertext = string(MarshalHeader(hdr)), ct
			sent[st.ID] = st
		case "recv", "forged":
			msg, ok := sent[st.ID]
			if !ok {
				t.Fatalf("%s delivered before it was sent", st.ID)
			}
			pt, err := decryptStep(sessions[st.To], msg, st.Op == "forged")
			if st.Op == "forged" {
				if err == nil {
					t.Fatalf("forged %s decrypted", st.ID)
				}
			} else if err != nil || string(pt) != msg.Plaintext {
				t.Fatalf("recv %s: %q, %v", st.ID, pt, err)
			}
		default:
			t.Fatalf("unknown op %q", st.Op)
		}
		out = append(out, st)
	}
	return out
}

func decryptStep(s *Session, msg vecStep, forge bool) ([]byte, error) {
	hdr, err := UnmarshalHeader([]byte(msg.Header))
	if err != nil {
		return nil, err
	}
	ct := append([]byte(nil), msg.Ciphertext...)
	if forge {
		ct[len(ct)-1] ^= 0x01
	}
	return s.Decrypt(hdr, ct)
}

// replayVectors holds the Go implementation to the file, exactly as each port
// holds itself to it.
func replayVectors(t *testing.T, v vectors) {
	t.Helper()
	load := func(label string, kp vecKeyPair) *KeyPair {
		k, err := keyPairFromPrivate(kp.Private)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if !bytes.Equal(k.PublicBytes(), kp.Public) {
			t.Fatalf("%s: public key does not match its private key", label)
		}
		return k
	}
	x := v.X3DH
	aIK, aEK := load("alice_identity", x.AliceIdentity), load("alice_ephemeral", x.AliceEphemeral)
	bIK, bSPK, bOPK := load("bob_identity", x.BobIdentity), load("bob_signed_prekey", x.BobSignedPre), load("bob_one_time_prekey", x.BobOneTimePre)

	signing := ed25519.NewKeyFromSeed(x.BobSigning.Seed)
	if !bytes.Equal(signing.Public().(ed25519.PublicKey), x.BobSigning.Public) {
		t.Fatal("bob_signing: public key does not match its seed")
	}
	if sig := SignPreKey(signing, bSPK.PublicBytes()); !bytes.Equal(sig, x.SignedPreKeySig) {
		t.Fatal("signed prekey signature differs")
	}

	bundle := PreKeyBundle{
		IdentityKey: bIK.PublicBytes(), SigningKey: x.BobSigning.Public,
		SignedPreKey: bSPK.PublicBytes(), SignedPreKeySig: x.SignedPreKeySig, OneTimePreKey: bOPK.PublicBytes(),
	}
	sk, ek, err := X3DHInitiator(InitiatorKeys{Identity: aIK, Ephemeral: aEK}, bundle)
	if err != nil || !bytes.Equal(sk, x.SharedSecret) {
		t.Fatalf("X3DH initiator shared secret differs (%v)", err)
	}
	skB, err := X3DHResponder(ResponderKeys{Identity: bIK, SignedPreKey: bSPK, OneTimePreKey: bOPK}, aIK.PublicBytes(), ek, true)
	if err != nil || !bytes.Equal(skB, x.SharedSecret) {
		t.Fatalf("X3DH responder shared secret differs (%v)", err)
	}
	skB, err = X3DHResponder(ResponderKeys{Identity: bIK, SignedPreKey: bSPK}, aIK.PublicBytes(), ek, false)
	if err != nil || !bytes.Equal(skB, x.SharedSecretNoOneTime) {
		t.Fatalf("X3DH shared secret without the one-time prekey differs (%v)", err)
	}

	source := func(label string, kps []vecKeyPair) *keySource {
		ks := &keySource{t: t}
		for i, kp := range kps {
			ks.keys = append(ks.keys, load(fmt.Sprintf("%s[%d]", label, i), kp))
		}
		return ks
	}
	aliceKeys := source("alice_ratchet_keys", v.Conversation.AliceRatchetKeys)
	bobKeys := source("bob_ratchet_keys", v.Conversation.BobRatchetKeys)
	alice, err := newInitiatorSession(x.SharedSecret, bSPK.PublicBytes(), aliceKeys.draw)
	if err != nil {
		t.Fatal(err)
	}
	bob := newResponderSession(x.SharedSecret, bSPK, bobKeys.draw)

	replayed := runScript(t, stripBytes(v.Conversation.Steps), map[string]*Session{"alice": alice, "bob": bob})
	for i, want := range v.Conversation.Steps {
		got := replayed[i]
		if got.Header != want.Header || !bytes.Equal(got.Ciphertext, want.Ciphertext) {
			t.Fatalf("step %d (%s %s): produced different bytes than the vectors", i, want.Op, want.ID)
		}
	}
	if aliceKeys.next != len(aliceKeys.keys) || bobKeys.next != len(bobKeys.keys) {
		t.Fatalf("ratchet keys drawn: alice %d/%d, bob %d/%d", aliceKeys.next, len(aliceKeys.keys), bobKeys.next, len(bobKeys.keys))
	}

	for _, s := range v.Safety {
		if got := SafetyNumber(s.LocalID, s.LocalIdentity, s.LocalSigning, s.RemoteID, s.RemoteIdent, s.RemoteSigning); got != s.Number {
			t.Fatalf("safety number for %s: %q, want %q", s.LocalID, got, s.Number)
		}
	}
}

// stripBytes keeps the script and drops the recorded bytes, so a replay has to
// produce them itself.
func stripBytes(steps []vecStep) []vecStep {
	out := make([]vecStep, len(steps))
	for i, s := range steps {
		s.Header, s.Ciphertext = "", nil
		out[i] = s
	}
	return out
}
