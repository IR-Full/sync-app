package envcfg

import (
	"os"
	"testing"
)

func TestGetReadsTheDocumentedSpelling(t *testing.T) {
	t.Setenv("SYNCAPP_EXAMPLE", "value")
	if got := Get("SYNCAPP_EXAMPLE"); got != "value" {
		t.Fatalf("Get = %q, want %q", got, "value")
	}
}

// The bug this package exists to prevent: call sites and documents disagreed on
// the spelling, so configuring a variable exactly as the README described it
// silently did nothing. The mixed-case spelling the code used before the rename
// still has to work for anyone who configured a deployment with it.
func TestGetFallsBackToTheLegacySpelling(t *testing.T) {
	t.Setenv("SyncApp_LEGACY_ONLY", "legacy value")
	if got := Get("SYNCAPP_LEGACY_ONLY"); got != "legacy value" {
		t.Fatalf("legacy fallback not honoured: %q", got)
	}
}

func TestDocumentedSpellingWinsOverLegacy(t *testing.T) {
	requireCaseSensitiveEnv(t)
	t.Setenv("SYNCAPP_BOTH", "current")
	t.Setenv("SyncApp_BOTH", "legacy")
	if got := Get("SYNCAPP_BOTH"); got != "current" {
		t.Fatalf("Get = %q, want the documented spelling to win", got)
	}
}

// requireCaseSensitiveEnv skips a test that needs the two spellings to be
// distinct variables.
//
// Windows treats environment variable names case-insensitively, so SyncApp_FOO
// and SYNCAPP_FOO are one variable there and the precedence question does not
// arise. That is also precisely why the original bug went unnoticed for so long:
// on a Windows dev machine the mismatched spellings worked, and only Linux — CI
// and production — saw the variable as unset.
func requireCaseSensitiveEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SyncApp_CaseProbe", "mixed")
	if os.Getenv("SYNCAPP_CASEPROBE") == "mixed" {
		t.Skip("environment variable names are case-insensitive on this platform")
	}
}

func TestGetUnsetIsEmpty(t *testing.T) {
	if got := Get("SyncApp_DEFINITELY_NOT_SET"); got != "" {
		t.Fatalf("unset variable returned %q", got)
	}
}

// A name without the prefix must not be mapped, or an unrelated variable could
// be shadowed by a SYNCAPP_-prefixed one.
func TestNonPrefixedNamesAreNotRemapped(t *testing.T) {
	requireCaseSensitiveEnv(t)
	t.Setenv("SYNCAPP_PATH", "should not be used")
	if got := Get("PATH_LIKE_NAME"); got != "" {
		t.Fatalf("a non-prefixed name resolved to %q", got)
	}
	if canonical, legacy := spellings("PLAIN_NAME"); canonical != "PLAIN_NAME" || legacy != "PLAIN_NAME" {
		t.Fatalf("a non-prefixed name was rewritten to %q / %q", canonical, legacy)
	}
}

func TestGetDefault(t *testing.T) {
	if got := GetDefault("SyncApp_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("GetDefault = %q, want the fallback", got)
	}
	t.Setenv("SyncApp_PRESENT", "set")
	if got := GetDefault("SyncApp_PRESENT", "fallback"); got != "set" {
		t.Fatalf("GetDefault = %q, want the set value", got)
	}
}

func TestInt(t *testing.T) {
	t.Setenv("SyncApp_NUMBER", "42")
	if got := Int("SyncApp_NUMBER", 7); got != 42 {
		t.Fatalf("Int = %d, want 42", got)
	}
	// An unparseable value falls back rather than failing the boot: a typo in a
	// tuning knob should not stop the server from starting.
	t.Setenv("SyncApp_NOT_A_NUMBER", "abc")
	if got := Int("SyncApp_NOT_A_NUMBER", 7); got != 7 {
		t.Fatalf("Int on garbage = %d, want the default", got)
	}
	if got := Int("SyncApp_UNSET_NUMBER", 7); got != 7 {
		t.Fatalf("Int unset = %d, want the default", got)
	}
}

func TestIntAcceptsLegacySpelling(t *testing.T) {
	t.Setenv("SyncApp_LEGACY_NUMBER", "13")
	if got := Int("SYNCAPP_LEGACY_NUMBER", 0); got != 13 {
		t.Fatalf("Int = %d, want the legacy value 13", got)
	}
}

func TestBoolAcceptsEverySpellingTheDocsUse(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "Yes"} {
		t.Setenv("SyncApp_FLAG", v)
		if !Bool("SyncApp_FLAG") {
			t.Fatalf("Bool(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "", "maybe"} {
		t.Setenv("SyncApp_FLAG", v)
		if Bool("SyncApp_FLAG") {
			t.Fatalf("Bool(%q) = true, want false", v)
		}
	}
}

// The mapping works from EITHER spelling, so a call site reads the same as
// whichever documentation it sits beside and still resolves both forms.
func TestSpellings(t *testing.T) {
	for _, given := range []string{"SyncApp_FOO", "SYNCAPP_FOO"} {
		canonical, legacy := spellings(given)
		if canonical != "SYNCAPP_FOO" || legacy != "SyncApp_FOO" {
			t.Errorf("spellings(%q) = %q / %q, want SYNCAPP_FOO / SyncApp_FOO", given, canonical, legacy)
		}
	}
}
