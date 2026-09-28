package totp

import (
	"testing"
	"time"
)

// TestRFC6238Vectors checks the implementation against the test vectors in the
// RFC's appendix.
//
// This is the test that matters most, and it matters because the alternative is
// self-consistency: an implementation that computes codes from its own secrets and
// verifies them will pass every round-trip test while producing codes no
// authenticator app agrees with. The vectors are the only thing that pins the
// construction to what a user's phone will show.
//
// The RFC publishes 8-digit codes for the ASCII secret "12345678901234567890";
// this package produces 6, so each expectation is the RFC value's last six digits.
func TestRFC6238Vectors(t *testing.T) {
	// The RFC's seed is the ASCII string, which in base32 is:
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	for _, tc := range []struct {
		unix int64
		want string // RFC 8-digit value, truncated to this package's 6
	}{
		{59, "287082"},         // RFC: 94287082
		{1111111109, "081804"}, // RFC: 07081804
		{1111111111, "050471"}, // RFC: 14050471
		{1234567890, "005924"}, // RFC: 89005924
		{2000000000, "279037"}, // RFC: 69279037
	} {
		got, err := Code(secret, time.Unix(tc.unix, 0))
		if err != nil {
			t.Fatalf("t=%d: %v", tc.unix, err)
		}
		if got != tc.want {
			t.Errorf("t=%d: code = %s, want %s (an authenticator app would disagree with us)",
				tc.unix, got, tc.want)
		}
	}
}

func TestVerifyAcceptsTheCurrentCode(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(secret, code, now) {
		t.Fatal("the current code did not verify")
	}
}

// TestVerifyToleratesClockSkew pins the window. A phone's clock drifts, and a
// code typed in the last second of its window arrives in the next one — so a
// verifier with no tolerance rejects codes that were correct when displayed.
func TestVerifyToleratesClockSkew(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Now()

	for _, skew := range []time.Duration{-period, 0, period} {
		code, _ := Code(secret, now.Add(skew))
		if !Verify(secret, code, now) {
			t.Errorf("a code from %v away was rejected", skew)
		}
	}
	// But not further. A wider window turns a stolen code into a usable one for
	// minutes rather than seconds.
	for _, skew := range []time.Duration{-3 * period, 3 * period} {
		code, _ := Code(secret, now.Add(skew))
		if Verify(secret, code, now) {
			t.Errorf("a code from %v away was accepted; the window is too wide", skew)
		}
	}
}

func TestVerifyRejectsWrongAndMalformedCodes(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Now()
	valid, _ := Code(secret, now)

	for name, code := range map[string]string{
		"empty":       "",
		"too short":   "12345",
		"too long":    "1234567",
		"letters":     "abcdef",
		"wrong value": "000000",
	} {
		if code == valid {
			continue // astronomically unlikely, but it would make the case vacuous
		}
		if Verify(secret, code, now) {
			t.Errorf("%s (%q) verified", name, code)
		}
	}

	// A code for a different secret must not verify.
	other, _ := GenerateSecret()
	otherCode, _ := Code(other, now)
	if otherCode != valid && Verify(secret, otherCode, now) {
		t.Error("a code for another secret verified")
	}
}

// TestVerifyAcceptsSecretsAsUsersPasteThem: apps display secrets in lowercase,
// in space-separated groups, and sometimes padded. All three are the same secret.
func TestVerifyAcceptsSecretsAsUsersPasteThem(t *testing.T) {
	const canonical = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := time.Unix(1111111109, 0)
	want, err := Code(canonical, now)
	if err != nil {
		t.Fatal(err)
	}

	for _, variant := range []string{
		"gezdgnbvgy3tqojqgezdgnbvgy3tqojq",
		"GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ",
		"  GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ  ",
		"GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ====",
	} {
		got, err := Code(variant, now)
		if err != nil {
			t.Errorf("%q: %v", variant, err)
			continue
		}
		if got != want {
			t.Errorf("%q produced %s, want %s", variant, got, want)
		}
	}
}

func TestGenerateSecretIsUniqueAndDecodable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s, err := GenerateSecret()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatal("GenerateSecret repeated a secret")
		}
		seen[s] = true
		if _, err := decodeSecret(s); err != nil {
			t.Fatalf("a generated secret does not decode: %v", err)
		}
	}
}

// TestProvisioningURICarriesWhatAppsRead guards the URI shape. Getting it wrong
// does not fail any code computation — it fails silently, in the user's
// authenticator, as an entry that produces codes the server rejects.
func TestProvisioningURICarriesWhatAppsRead(t *testing.T) {
	uri := ProvisioningURI("SyncApp", "alice", "GEZDGNBVGY3TQOJQ")

	for _, want := range []string{
		"otpauth://totp/",
		"SyncApp",
		"alice",
		"secret=GEZDGNBVGY3TQOJQ",
		"issuer=SyncApp",
		"algorithm=SHA1",
		"digits=6",
		"period=30",
	} {
		if !contains(uri, want) {
			t.Errorf("URI %q is missing %q", uri, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
