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
	ad := hdr.bytes()
	ct, err := aeadSeal(mk, ad, plaintext)
	if err != nil {
		return Header{}, nil, err
	}
	return hdr, ct, nil
}

// Decrypt ratchet-decrypts a message, performing a DH ratchet step and/or
// skipped-key handling as needed.
func (s *Session) Decrypt(hdr Header, ciphertext []byte) ([]byte, error) {
	// 1. If we have a stored skipped key for this header, use it.
	if pt, ok := s.trySkipped(hdr, ciphertext); ok {
		return pt, nil
	}
	// 2. If the header advertises a new ratchet key, perform a DH ratchet.
	if !s.sameDHr(hdr.DH) {
		if err := s.skipMessageKeys(hdr.PN); err != nil {
			return nil, err
		}
		if err := s.dhRatchet(hdr); err != nil {
			return nil, err
		}
	}
	// 3. Skip any messages before this one in the current receiving chain.
	if err := s.skipMessageKeys(hdr.N); err != nil {
		return nil, err
	}
	// 4. Derive this message's key and decrypt.
	var mk []byte
	s.ckr, mk = kdfCK(s.ckr)
	s.nr++
	pt, err := aeadOpen(mk, hdr.bytes(), ciphertext)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
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
		s.skipped[skKey(s.dhr.Bytes(), s.nr)] = mk
		s.nr++
	}
	return nil
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
	r := hkdf.New(sha256.New, dhOut, rk, []byte("Synapse-Ratchet-RK"))
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
	r := hkdf.New(sha256.New, mk, nil, []byte("Synapse-Ratchet-Msg"))
	if _, err = io.ReadFull(r, buf); err != nil {
		return nil, nil, err
	}
	c, err := chacha20poly1305.New(buf[:chacha20poly1305.KeySize])
	if err != nil {
		return nil, nil, err
	}
	return c, buf[chacha20poly1305.KeySize:], nil
}

func (h Header) bytes() []byte {
	b, _ := json.Marshal(h)
	return b
}

// MarshalHeader / UnmarshalHeader serialize a header for the wire.
func MarshalHeader(h Header) []byte { return h.bytes() }

// UnmarshalHeader parses a header from the wire.
func UnmarshalHeader(b []byte) (Header, error) {
	var h Header
	err := json.Unmarshal(b, &h)
	return h, err
}
