package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// NewInitiatorSession starts Alice's session after X3DH. sk is the shared secret;
// theirSignedPreKey is Bob's signed prekey public (Alice's initial DHr).
func NewInitiatorSession(sk, theirSignedPreKey []byte) (*Session, error) {
	dhr, err := PublicKeyFromBytes(theirSignedPreKey)
	if err != nil {
		return nil, err
	}
	dhs, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	s := &Session{dhs: dhs, dhr: dhr, rk: sk, skipped: map[string][]byte{}}
	// Perform the initial DH ratchet so Alice has a sending chain.
	dhOut, err := dh(s.dhs.Priv, s.dhr)
	if err != nil {
		return nil, err
	}
	s.rk, s.cks = kdfRK(s.rk, dhOut)
	return s, nil
}

// NewResponderSession starts Bob's session. sk is the shared secret; signedPreKey
// is Bob's signed prekey key pair (his initial DHs). Bob has no sending chain
// until he receives Alice's first message and ratchets.
func NewResponderSession(sk []byte, signedPreKey *KeyPair) (*Session, error) {
	return &Session{dhs: signedPreKey, rk: sk, skipped: map[string][]byte{}}, nil
}

// Encrypt ratchet-encrypts plaintext, returning the header and ciphertext.
func (s *Session) Encrypt(plaintext []byte) (Header, []byte, error) {
	var mk []byte
	s.cks, mk = kdfCK(s.cks)
	hdr := Header{DH: s.dhs.PublicBytes(), PN: s.pn, N: s.ns}
	s.ns++
	// Pin the serialization used as additional data onto the header, so whatever
	// the caller puts on the wire (MarshalHeader) is byte-for-byte what this
	// AEAD authenticated. Without it the two are produced by two separate
	// marshal calls that are only equal by convention.
	ad := hdr.bytes()
	hdr.raw = ad
	ct, err := aeadSeal(mk, ad, plaintext)
	if err != nil {
		return Header{}, nil, err
	}
	return hdr, ct, nil
}

// Decrypt ratchet-decrypts a message, performing a DH ratchet step and/or
// skipped-key handling as needed.
//
// **No state moves until the AEAD says the message is genuine.** That ordering
// is the whole point of this function's shape, and getting it wrong is a real
// attack rather than a tidiness concern: the header is written by whoever sent
// the frame, and the relay (MsgSecretSend) lets any account address any device.
// An earlier version ran the DH ratchet and the skipped-key run first and
// decrypted afterwards, so a single forged frame carrying a random ratchet key
// overwrote rk/ckr/cks/dhs/dhr — permanently destroying a live session with the
// real peer, and costing up to 2*maxSkip key derivations on the way. The Double
// Ratchet spec calls for exactly this: apply to a copy, commit on success.
func (s *Session) Decrypt(hdr Header, ciphertext []byte) ([]byte, error) {
	// 1. A key stored earlier for a message that arrived out of order. Consuming
	//    one is already commit-on-success (trySkipped deletes only after the AEAD
	//    opens), so it needs no staging.
	if pt, ok := s.trySkipped(hdr, ciphertext); ok {
		return pt, nil
	}

	// 2. The ordinary case: the peer's current ratchet key, the next message in
	//    the chain. Nothing is skipped and no DH step is needed, so the only
	//    state that moves is the receiving chain — held back in two locals until
	//    the message authenticates. Kept separate from the staged path below
	//    because this is the branch every in-order message takes, and it should
	//    not pay to copy the skipped-key map.
	if s.ckr != nil && s.sameDHr(hdr.DH) && hdr.N == s.nr {
		ckr, mk := kdfCK(s.ckr)
		pt, err := aeadOpen(mk, hdr.bytes(), ciphertext)
		if err != nil {
			return nil, ErrDecrypt
		}
		s.ckr, s.nr = ckr, s.nr+1
		return pt, nil
	}

	// 3. Everything else — a DH ratchet step, a gap to skip, or both — runs
	//    against a COPY. A forged header therefore costs one wasted copy and is
	//    discarded; only a frame that authenticates is adopted.
	trial := s.clone()
	pt, err := trial.advance(hdr, ciphertext)
	if err != nil {
		return nil, err
	}
	*s = *trial
	return pt, nil
}

// advance is the ratchet+skip path, run on a throwaway copy by Decrypt. It may
// mutate the receiver freely: nothing it touches is the caller's session until
// Decrypt adopts it.
func (s *Session) advance(hdr Header, ciphertext []byte) ([]byte, error) {
	// If the header advertises a new ratchet key, perform a DH ratchet.
	if !s.sameDHr(hdr.DH) {
		if err := s.skipMessageKeys(hdr.PN); err != nil {
			return nil, err
		}
		if err := s.dhRatchet(hdr); err != nil {
			return nil, err
		}
	}
	// Skip any messages before this one in the current receiving chain.
	if err := s.skipMessageKeys(hdr.N); err != nil {
		return nil, err
	}
	if s.ckr == nil {
		return nil, ErrDecrypt
	}
	// Derive this message's key and decrypt.
	var mk []byte
	s.ckr, mk = kdfCK(s.ckr)
	s.nr++
	pt, err := aeadOpen(mk, hdr.bytes(), ciphertext)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// clone returns a copy safe to mutate independently of s.
//
// The byte slices are shared deliberately: kdfRK and kdfCK always return freshly
// allocated slices and never write through the ones they are given, so a chain
// key is replaced rather than modified and the two copies cannot alias a value
// either of them will change. dhs and dhr are replaced wholesale by dhRatchet
// for the same reason. The skipped-key map is the one thing that IS written in
// place, so it is the one thing copied.
func (s *Session) clone() *Session {
	c := *s
	c.skipped = make(map[string][]byte, len(s.skipped))
	for k, v := range s.skipped {
		c.skipped[k] = v
	}
	c.skippedOrder = append([]string(nil), s.skippedOrder...)
	return &c
}

func (s *Session) sameDHr(dhPub []byte) bool {
	return s.dhr != nil && string(s.dhr.Bytes()) == string(dhPub)
}

// dhRatchet advances to the peer's new ratchet key: derive a new receiving
// chain, then a new sending chain with a fresh local ratchet key.
func (s *Session) dhRatchet(hdr Header) error {
	newDHr, err := PublicKeyFromBytes(hdr.DH)
	if err != nil {
		return err
	}
	s.pn = s.ns
	s.ns = 0
	s.nr = 0
	s.dhr = newDHr

	dhOut, err := dh(s.dhs.Priv, s.dhr)
	if err != nil {
		return err
	}
	s.rk, s.ckr = kdfRK(s.rk, dhOut)

	// Generate a new local ratchet key pair and derive the new sending chain.
	s.dhs, err = GenerateKeyPair()
	if err != nil {
		return err
	}
	dhOut2, err := dh(s.dhs.Priv, s.dhr)
	if err != nil {
		return err
	}
	s.rk, s.cks = kdfRK(s.rk, dhOut2)
	return nil
}

// skipMessageKeys derives and stores message keys up to (not including) until,
// so out-of-order / missing messages can still be decrypted later.
func (s *Session) skipMessageKeys(until uint32) error {
	if s.ckr == nil {
		return nil
	}
	if until-s.nr > uint32(maxSkip) {
		return errors.New("e2e: too many skipped messages")
	}
	for s.nr < until {
		var mk []byte
		s.ckr, mk = kdfCK(s.ckr)
		s.storeSkipped(skKey(s.dhr.Bytes(), s.nr), mk)
		s.nr++
	}
	return nil
}

// storeSkipped records a message key for a gap, evicting the oldest once the
// store is full.
//
// maxSkip bounds ONE call; without a ceiling on the total the map still grew
// without limit, because every DH ratchet step starts the count again — and the
// web client persists this map, so the growth outlived the process. A bound has
// to drop something, and the oldest key is the right thing to drop: a message
// that has not arrived after thousands of later ones is not arriving, and the
// cost of being wrong is one undecryptable message rather than a session.
func (s *Session) storeSkipped(key string, mk []byte) {
	if _, exists := s.skipped[key]; !exists {
		s.skippedOrder = append(s.skippedOrder, key)
	}
	s.skipped[key] = mk
	for len(s.skipped) > maxSkippedKeys && len(s.skippedOrder) > 0 {
		oldest := s.skippedOrder[0]
		s.skippedOrder = s.skippedOrder[1:]
		delete(s.skipped, oldest)
	}
}

func (s *Session) trySkipped(hdr Header, ct []byte) ([]byte, bool) {
	key := skKey(hdr.DH, hdr.N)
	mk, ok := s.skipped[key]
	if !ok {
		return nil, false
	}
	pt, err := aeadOpen(mk, hdr.bytes(), ct)
	if err != nil {
		return nil, false
	}
	delete(s.skipped, key)
	// skippedOrder keeps its entry: it is an eviction order, not an index, and
	// storeSkipped tolerates a name whose key is already gone. Scanning the slice
	// to remove one string on every successful out-of-order decrypt would cost
	// more than the string it saves.
	return pt, true
}

func skKey(dhPub []byte, n uint32) string {
	var nb [4]byte
	binary.BigEndian.PutUint32(nb[:], n)
	return string(dhPub) + "|" + string(nb[:])
}

// --- KDFs ---

// kdfRK derives (newRootKey, chainKey) from the root key and a DH output.
func kdfRK(rk, dhOut []byte) (newRK, ck []byte) {
	out := make([]byte, 64)
	r := hkdf.New(sha256.New, dhOut, rk, []byte("SyncApp-Ratchet-RK"))
	_, _ = io.ReadFull(r, out)
	return out[:32], out[32:]
}

// kdfCK advances a chain key: HMAC with constant inputs gives (newCK, messageKey).
func kdfCK(ck []byte) (newCK, mk []byte) {
	mac := hmac.New(sha256.New, ck)
	mac.Write([]byte{0x02})
	newCK = mac.Sum(nil)
	mac.Reset()
	mac.Write([]byte{0x01})
	mk = mac.Sum(nil)
	return newCK, mk
}

// --- AEAD (ChaCha20-Poly1305) ---

func aeadSeal(mk, ad, plaintext []byte) ([]byte, error) {
	aead, nonce, err := aeadFor(mk)
	if err != nil {
		return nil, err
	}
	// Deterministic zero nonce is safe: each message key is unique (used once).
	return aead.Seal(nonce[:0], nonce, plaintext, ad), nil
}

func aeadOpen(mk, ad, ciphertext []byte) ([]byte, error) {
	aead, nonce, err := aeadFor(mk)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, ad)
}

func aeadFor(mk []byte) (aead interface {
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}, nonce []byte, err error) {
	// Derive an AEAD key + nonce from the message key via HKDF so the message key
	// itself is never used directly as the cipher key.
	buf := make([]byte, chacha20poly1305.KeySize+chacha20poly1305.NonceSize)
	r := hkdf.New(sha256.New, mk, nil, []byte("SyncApp-Ratchet-Msg"))
	if _, err = io.ReadFull(r, buf); err != nil {
		return nil, nil, err
	}
	c, err := chacha20poly1305.New(buf[:chacha20poly1305.KeySize])
	if err != nil {
		return nil, nil, err
	}
	return c, buf[chacha20poly1305.KeySize:], nil
}

// bytes is the header's wire form, and therefore the AEAD's additional data.
// It returns the exact serialization the header came with when there is one
// (see Header.raw); only a header this process built itself is marshalled here.
func (h Header) bytes() []byte {
	if h.raw != nil {
		return h.raw
	}
	b, _ := json.Marshal(h)
	return b
}

// MarshalHeader / UnmarshalHeader serialize a header for the wire.
func MarshalHeader(h Header) []byte { return h.bytes() }

// UnmarshalHeader parses a header from the wire, remembering the bytes it came
// from so they — and not a re-encoding of them — are what gets authenticated.
func UnmarshalHeader(b []byte) (Header, error) {
	var h Header
	if err := json.Unmarshal(b, &h); err != nil {
		return Header{}, err
	}
	// Copied, because b belongs to the caller's read buffer and this value
	// outlives the call.
	h.raw = append([]byte(nil), b...)
	return h, nil
}
