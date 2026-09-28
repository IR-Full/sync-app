package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/totp"
)

/*
The second factor.

The first factor was the only one, and it was interceptable — which is why
Telegram has a cloud password and why every account system that survives contact
with users grows one. The design decisions worth stating:

**Enrolment is two steps.** BeginTOTP mints a secret and returns it; ConfirmTOTP
requires a code computed FROM that secret before the factor is enforced. Without
the second step a mis-scanned QR code locks the account out of itself, which is
the single most common way a 2FA rollout goes wrong.

**The secret is encrypted, not hashed.** Verification has to recompute a code, so
it needs the secret back — that is the difference between a secret and a
credential. The key lives in the environment, so a database dump is not a dump of
everyone's second factor.

**Recovery codes are hashed, not encrypted.** They are never needed back: checking
one compares a hash. A leak of that table yields nothing usable.

**Disabling requires the password AND a code.** Someone holding only a stolen
session token must not be able to remove the factor that is keeping them out of
the next login.
*/

// BeginTOTP mints a secret and returns it with its provisioning URI. The factor
// is NOT yet enforced — ConfirmTOTP does that.
//
// Re-enrolling over an unconfirmed secret is allowed and replaces it: a user who
// closed the setup screen and came back should get a working QR code, not an
// error about a secret they never managed to save.
//
// Re-enrolling over a CONFIRMED one is refused. Silently replacing a working
// factor would let a stolen session swap the factor for its own, which is the
// same attack DisableTOTP's password requirement exists to stop.
func (s *Service) BeginTOTP(ctx context.Context, userID, issuer string) (secret, uri string, err error) {
	tfStore, ok := s.twoFactorStore()
	if !ok {
		return "", "", store.ErrUnsupported
	}
	key, err := totpKey()
	if err != nil {
		// Refusing is the point. Storing the secret in the clear because the
		// operator forgot a key would make the database dump the thing this factor
		// is supposed to survive.
		return "", "", err
	}
	if existing, err := tfStore.GetTwoFactor(ctx, userID); err == nil && existing.Enabled() {
		return "", "", ErrTwoFactorEnabled
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", "", err
	}

	u, err := s.users.GetUser(ctx, userID)
	if err != nil {
		return "", "", err
	}
	secret, err = totp.GenerateSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := sealSecret(key, secret)
	if err != nil {
		return "", "", err
	}
	if err := tfStore.PutTwoFactor(ctx, &model.TwoFactor{
		UserID: userID, SecretEnc: enc, CreatedAt: nowMs(),
	}); err != nil {
		return "", "", err
	}
	return secret, totp.ProvisioningURI(issuer, u.Username, secret), nil
}

// ConfirmTOTP enforces the factor once the user proves they can produce a code,
// and returns the recovery codes. They are returned ONCE and never again — the
// stored form is a hash, so there is nothing to show later.
func (s *Service) ConfirmTOTP(ctx context.Context, userID, code string) ([]string, error) {
	tfStore, ok := s.twoFactorStore()
	if !ok {
		return nil, store.ErrUnsupported
	}
	key, err := totpKey()
	if err != nil {
		return nil, err
	}
	tf, err := tfStore.GetTwoFactor(ctx, userID)
	if err != nil {
		return nil, err
	}
	if tf.Enabled() {
		return nil, ErrTwoFactorEnabled
	}
	secret, err := openSecret(key, tf.SecretEnc)
	if err != nil {
		return nil, err
	}
	if !totp.Verify(secret, code, time.Now()) {
		return nil, ErrBadTOTPCode
	}

	plain, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	tf.ConfirmedAt = nowMs()
	tf.RecoveryHashes = hashes
	if err := tfStore.PutTwoFactor(ctx, tf); err != nil {
		return nil, err
	}
	return plain, nil
}

// DisableTOTP removes the factor. Requires the password AND a live code (or a
// recovery code), because a stolen session token must not be able to take the
// factor off the account it is locked out of next time.
func (s *Service) DisableTOTP(ctx context.Context, userID, password, code string) error {
	tfStore, ok := s.twoFactorStore()
	if !ok {
		return store.ErrUnsupported
	}
	u, err := s.users.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if !verifyPassword(password, u.PasswordHash) {
		return ErrWrongPassword
	}
	ok, err = s.verifySecondFactor(ctx, userID, code)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadTOTPCode
	}
	return tfStore.DeleteTwoFactor(ctx, userID)
}

// TwoFactorEnabled reports whether an account enforces a second factor. Used by
// the login path to decide whether a code is required, and by the client to draw
// the settings screen.
func (s *Service) TwoFactorEnabled(ctx context.Context, userID string) bool {
	tfStore, ok := s.twoFactorStore()
	if !ok {
		return false
	}
	tf, err := tfStore.GetTwoFactor(ctx, userID)
	return err == nil && tf.Enabled()
}

// RecoveryCodesLeft is how many unspent recovery codes remain, so a client can
// warn before the last one is gone. Losing the final code and the phone together
// is the state there is no way back from.
func (s *Service) RecoveryCodesLeft(ctx context.Context, userID string) int {
	tfStore, ok := s.twoFactorStore()
	if !ok {
		return 0
	}
	tf, err := tfStore.GetTwoFactor(ctx, userID)
	if err != nil {
		return 0
	}
	return len(tf.RecoveryHashes)
}

// verifySecondFactor accepts either a live TOTP code or a recovery code.
//
// TOTP is tried first because it is the common case and costs one HMAC; recovery
// codes cost an argon2id verify per stored hash, which is deliberately expensive
// and should not be paid on every login.
func (s *Service) verifySecondFactor(ctx context.Context, userID, code string) (bool, error) {
	tfStore, ok := s.twoFactorStore()
	if !ok {
		return false, store.ErrUnsupported
	}
	tf, err := tfStore.GetTwoFactor(ctx, userID)
	if err != nil {
		return false, err
	}
	key, err := totpKey()
	if err != nil {
		return false, err
	}
	if secret, err := openSecret(key, tf.SecretEnc); err == nil {
		if totp.Verify(secret, code, time.Now()) {
			return true, nil
		}
	}
	// Recovery codes are compared against every stored hash, and the matching one
	// is CONSUMED by the store rather than here: single-use plus check-then-write
	// would let two concurrent logins spend the same code.
	normalized := normalizeRecoveryCode(code)
	for _, h := range tf.RecoveryHashes {
		if !verifyPassword(normalized, h) {
			continue
		}
		spent, err := tfStore.ConsumeRecoveryCode(ctx, userID, h)
		if err != nil {
			return false, err
		}
		return spent, nil
	}
	return false, nil
}

// --- secret sealing ---

// totpKey reads the at-rest encryption key for TOTP secrets.
//
// An error rather than a fallback. There is no safe default here: generating a
// key per process would make every restart invalidate every enrolment, and using
// a constant would make the ciphertext decorative. An operator who enables a
// second factor without configuring a key gets told, at the point of enrolment,
// rather than discovering it in a breach.
func totpKey() ([]byte, error) {
	raw := strings.TrimSpace(envcfg.Get("SYNCAPP_TOTP_KEY"))
	if raw == "" {
		return nil, ErrNoTOTPKey
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// Also accept raw 32-byte keys, since operators paste both.
		key = []byte(raw)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: need 32 bytes (base64 or raw), got %d", ErrNoTOTPKey, len(key))
	}
	return key, nil
}

// sealSecret encrypts a TOTP secret with AES-256-GCM, nonce prefixed.
func sealSecret(key []byte, secret string) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("auth: cannot generate a nonce: %w", err)
	}
	ct := aead.Seal(nonce, nonce, []byte(secret), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func openSecret(key []byte, sealed string) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("auth: stored secret is not base64: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("auth: stored secret is truncated")
	}
	nonce, ct := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("auth: stored secret does not decrypt: %w", err)
	}
	return string(pt), nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// --- recovery codes ---

// newRecoveryCodes mints the plaintext codes (returned once) and their hashes
// (stored).
//
// Base32 without the ambiguous characters, in two groups, because these get
// written down on paper and read back by a human: '0' and 'O', '1' and 'I' are
// the same glyph in most handwriting, and a code nobody can transcribe is not a
// recovery path.
func newRecoveryCodes() (plain, hashes []string, err error) {
	for i := 0; i < recoveryCodeCount; i++ {
		b := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("auth: cannot generate a recovery code: %w", err)
		}
		raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
		raw = strings.Map(unambiguous, raw)[:recoveryCodeLen]
		code := raw[:recoveryCodeLen/2] + "-" + raw[recoveryCodeLen/2:]

		// argon2id, the same as a password, because that is what these are: a
		// credential a human typed. A fast hash would make the stored set
		// brute-forceable, and the set is small enough to be worth attacking.
		h, err := hashPassword(normalizeRecoveryCode(code))
		if err != nil {
			return nil, nil, err
		}
		plain = append(plain, code)
		hashes = append(hashes, h)
	}
	return plain, hashes, nil
}

// unambiguous replaces glyphs that are indistinguishable on paper.
func unambiguous(r rune) rune {
	switch r {
	case '0', 'O':
		return '8'
	case '1', 'I', 'L':
		return '9'
	}
	return r
}

// normalizeRecoveryCode accepts the code however it was transcribed: any case,
// with or without the separator, with stray spaces.
func normalizeRecoveryCode(code string) string {
	s := strings.ToUpper(strings.TrimSpace(code))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// twoFactorStore is the optional capability lookup, in one place so every method
// degrades the same way.
func (s *Service) twoFactorStore() (store.TwoFactorStore, bool) {
	if s.twoFactor == nil {
		return nil, false
	}
	return s.twoFactor, true
}

var (
	// ErrTwoFactorEnabled means the account already enforces a second factor.
	ErrTwoFactorEnabled = errors.New("auth: two-factor is already enabled")
	// ErrTwoFactorRequired means the password was right but a code is needed.
	// Distinct from ErrBadCredentials so a client shows a code prompt instead of
	// telling the user their password is wrong.
	ErrTwoFactorRequired = errors.New("auth: two-factor code required")
	// ErrBadTOTPCode means the code (or recovery code) did not verify.
	ErrBadTOTPCode = errors.New("auth: invalid two-factor code")
	// ErrNoTOTPKey means SYNCAPP_TOTP_KEY is unset or malformed, so secrets
	// cannot be stored encrypted — and are therefore not stored at all.
	ErrNoTOTPKey = errors.New("auth: SYNCAPP_TOTP_KEY is not configured")
)
