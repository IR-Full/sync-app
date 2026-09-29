// Package totp implements RFC 6238 time-based one-time passwords, and RFC 4648
// base32 secrets for the provisioning URIs authenticator apps read.
//
// Hand-rolled rather than pulled in as a dependency, for the same reason this
// project hand-rolled its wire codec and its ratchet: the whole algorithm is an
// HMAC, a truncation and a modulo, so a dependency here buys nothing and adds a
// package that sits in the authentication path of every login.
//
// SHA-1 is the hash, and that is not an oversight. RFC 6238 permits SHA-256 and
// SHA-512, but every authenticator app in practice assumes SHA-1 for a URI that
// does not say otherwise, and several ignore the algorithm parameter entirely —
// so choosing "stronger" here produces codes that do not match what the user's
// phone shows. The construction does not depend on collision resistance: it is
// an HMAC over a counter with a shared secret, and the output is six digits
// valid for thirty seconds.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 HMAC-SHA1; see the package comment
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// digits is the code length. Six, because that is what every authenticator
	// app shows and what users expect to type; eight is permitted by RFC 6238 and
	// supported by almost nothing.
	digits = 6
	// period is one time step. Thirty seconds is the RFC default and, again, what
	// apps assume for a URI that does not say otherwise.
	period = 30 * time.Second
	// secretBytes is the shared-secret length. Twenty bytes matches SHA-1's output
	// size, which is the size RFC 4226 recommends and every app handles.
	secretBytes = 20
)

// GenerateSecret returns a fresh base32 secret.
//
// Twenty bytes, which is the SHA-1 block-adjacent size RFC 4226 recommends and
// what every authenticator expects. The error from crypto/rand is returned
// rather than ignored: a failing entropy source would otherwise hand out a
// zeroed secret, and the rest of the flow would happily enrol it — the same trap
// the session-token generator documents.
func GenerateSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("totp: cannot generate a secret: %w", err)
	}
	// No padding: authenticator apps and the otpauth URI convention both expect
	// an unpadded secret, and several reject the '=' characters outright.
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// Code computes the code for a secret at a point in time.
func Code(secret string, at time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return codeAtCounter(key, uint64(at.Unix())/uint64(period.Seconds())), nil
}

// Verify reports whether code is valid for secret at time now.
//
// It accepts the neighbouring windows as well as the current one. That is not
// laxity: a phone's clock drifts, and a user who types a code in the last second
// of its window submits it in the next one. The window is ±1 step (thirty
// seconds either side), which is the usual compromise — wider turns a stolen
// code into a usable one for minutes.
//
// The comparison is constant time. A code is a six-digit secret with a short
// life, and an early-exit compare leaks how much of a guess was right, which is
// exactly the hint that makes guessing cheap.
func Verify(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return false
	}
	key, err := decodeSecret(secret)
	if err != nil {
		return false
	}
	counter := uint64(now.Unix()) / uint64(period.Seconds())
	for _, delta := range []int64{0, -1, 1} {
		c := int64(counter) + delta
		if c < 0 {
			continue
		}
		want := codeAtCounter(key, uint64(c))
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// ProvisioningURI builds the otpauth:// URI an authenticator app scans.
//
// issuer appears twice — once as the label prefix and once as a parameter —
// because that is what the de-facto spec (Google's Key URI Format) requires for
// apps to group entries correctly; the ones that read only the label and the ones
// that read only the parameter both need it.
func ProvisioningURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", digits))
	q.Set("period", fmt.Sprintf("%d", int(period.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// codeAtCounter is the HOTP construction of RFC 4226: HMAC the 8-byte
// big-endian counter, take the low nibble of the last byte as an offset, read a
// 31-bit integer from there, and reduce it to the digit count.
func codeAtCounter(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	// The high bit is masked off so the value is positive regardless of platform
	// signedness — the "dynamic truncation" step the RFC spells out.
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", digits, truncated%pow10(digits))
}

// decodeSecret accepts the secret in the shape users paste it: upper or lower
// case, with or without the spaces some apps insert for legibility, padded or
// not.
func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	s = strings.TrimRight(s, "=")
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("totp: bad secret: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("totp: empty secret")
	}
	return key, nil
}

func pow10(n int) uint32 {
	out := uint32(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}
