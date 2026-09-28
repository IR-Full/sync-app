package platform

import (
	"strings"
	"testing"
)

// safeProduction sets every variable the preflight demands, so a test can then
// unset exactly one and assert that it alone is what fails.
func safeProduction(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"SYNCAPP_REQUIRE_TLS":        "1",
		"SYNCAPP_TLS_CERT":           "/etc/certs/tls.crt",
		"SYNCAPP_TLS_KEY":            "/etc/certs/tls.key",
		"SYNCAPP_TLS_SELFSIGNED":     "",
		"SYNCAPP_MEDIA_SECRET":       "a-real-secret-from-a-secrets-manager",
		"SYNCAPP_ALLOWED_ORIGINS":    "https://app.example",
		"SYNCAPP_MAX_CONNS_PER_IP":   "64",
		"SYNCAPP_ACCEPT_RATE_PER_IP": "20",
	} {
		t.Setenv(k, v)
	}
}

func TestProductionPreflightPassesOnASafeConfig(t *testing.T) {
	safeProduction(t)
	if problems := CheckProduction(); len(problems) != 0 {
		t.Fatalf("a safe config was rejected: %v", problems)
	}
	if err := EnforceProduction(func(string, ...any) {}); err != nil {
		t.Fatalf("enforceProduction: %v", err)
	}
}

// Each of these is fine in development and a real vulnerability in production.
// They are checked one at a time so a failure names the setting rather than the
// whole list.
func TestProductionPreflightCatchesEachUnsafeSetting(t *testing.T) {
	cases := map[string]struct {
		key, value string
		wantSubstr string
	}{
		"plaintext transport": {
			"SYNCAPP_TLS_CERT", "", "plaintext",
		},
		"self-signed certificate": {
			"SYNCAPP_TLS_SELFSIGNED", "1", "development certificate",
		},
		"default media secret": {
			"SYNCAPP_MEDIA_SECRET", "", "forge upload and download links",
		},
		"media secret copied from the logs": {
			"SYNCAPP_MEDIA_SECRET", "dev-insecure-media-secret-change-me", "development default",
		},
		"any origin accepted": {
			"SYNCAPP_ALLOWED_ORIGINS", "", "cross-site WebSocket hijacking",
		},
		"no connection cap": {
			"SYNCAPP_MAX_CONNS_PER_IP", "", "unlimited connections",
		},
		"connection cap not a number": {
			"SYNCAPP_MAX_CONNS_PER_IP", "lots", "unlimited connections",
		},
		"no accept rate cap": {
			"SYNCAPP_ACCEPT_RATE_PER_IP", "0", "line rate",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			safeProduction(t)
			// The self-signed case needs the cert pair cleared as well, otherwise the
			// explicit files win and the flag is never consulted.
			if tc.key == "SYNCAPP_TLS_SELFSIGNED" {
				t.Setenv("SYNCAPP_TLS_CERT", "")
				t.Setenv("SYNCAPP_TLS_KEY", "")
			}
			t.Setenv(tc.key, tc.value)

			problems := CheckProduction()
			if len(problems) == 0 {
				t.Fatalf("%s was accepted", name)
			}
			if !containsSubstr(problems, tc.wantSubstr) {
				t.Fatalf("no problem mentioned %q; got %v", tc.wantSubstr, problems)
			}

			err := EnforceProduction(func(string, ...any) {})
			if err == nil {
				t.Fatal("enforceProduction accepted an unsafe production config")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error did not explain the problem: %v", err)
			}
		})
	}
}

// Every violation has to be reported at once. Failing on the first would have an
// operator fix it, redeploy, and discover the next — three deploys to learn what
// one message could say.
func TestProductionPreflightReportsEveryProblemAtOnce(t *testing.T) {
	t.Setenv("SYNCAPP_REQUIRE_TLS", "1")
	for _, k := range []string{
		"SYNCAPP_TLS_CERT", "SYNCAPP_TLS_KEY", "SYNCAPP_TLS_SELFSIGNED",
		"SYNCAPP_MEDIA_SECRET", "SYNCAPP_ALLOWED_ORIGINS",
		"SYNCAPP_MAX_CONNS_PER_IP", "SYNCAPP_ACCEPT_RATE_PER_IP",
	} {
		t.Setenv(k, "")
	}

	problems := CheckProduction()
	if len(problems) != 5 {
		t.Fatalf("expected all 5 violations, got %d: %v", len(problems), problems)
	}
	err := EnforceProduction(func(string, ...any) {})
	if err == nil {
		t.Fatal("an entirely unsafe config booted")
	}
	for _, want := range []string{"plaintext", "forge upload", "hijacking", "unlimited connections", "line rate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the combined error never mentions %q:\n%v", want, err)
		}
	}
}

// Outside production the same problems are warnings, not a refusal: a developer
// must be able to run the server with nothing configured, while still being told
// exactly what a real deployment would reject.
func TestProductionPreflightOnlyWarnsOutsideProduction(t *testing.T) {
	t.Setenv("SYNCAPP_REQUIRE_TLS", "")
	for _, k := range []string{
		"SYNCAPP_TLS_CERT", "SYNCAPP_TLS_KEY", "SYNCAPP_MEDIA_SECRET",
		"SYNCAPP_ALLOWED_ORIGINS", "SYNCAPP_MAX_CONNS_PER_IP", "SYNCAPP_ACCEPT_RATE_PER_IP",
	} {
		t.Setenv(k, "")
	}

	var warnings int
	if err := EnforceProduction(func(string, ...any) { warnings++ }); err != nil {
		t.Fatalf("development boot was refused: %v", err)
	}
	if warnings == 0 {
		t.Fatal("a wholly unconfigured development boot said nothing at all")
	}
}

// The flag has three accepted spellings, and every consumer must agree on all
// three. They did not: two copies of this predicate accepted "1"/"true"/"yes"
// while gatewayd compared against the literal "1", so SYNCAPP_REQUIRE_TLS=true
// declared production for the node-id guard and development for TLS enforcement
// in the same process.
func TestRequireTLSAcceptsEverySpellingOfYes(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "True", "yes", "YES"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("SYNCAPP_REQUIRE_TLS", v)
			if !RequireTLS() {
				t.Errorf("SYNCAPP_REQUIRE_TLS=%q did not declare production", v)
			}
		})
	}
}

func TestRequireTLSIsOffByDefault(t *testing.T) {
	// A developer who sets nothing must get a machine they can run, and the
	// preflight must degrade to warnings rather than refuse the boot.
	for _, v := range []string{"", "0", "false", "no"} {
		t.Setenv("SYNCAPP_REQUIRE_TLS", v)
		if RequireTLS() {
			t.Errorf("SYNCAPP_REQUIRE_TLS=%q declared production", v)
		}
	}
}

func containsSubstr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
