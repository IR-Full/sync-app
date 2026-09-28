package envcfg

import "testing"

/*
The alias exists so a deployment configured before the rename keeps working, and
so an operator who types the POSIX-conventional SHOUTING name gets what the
documentation promises. Both directions have to work, from either spelling of
the name a caller passes — otherwise the alias is real for some variables and
not others, which is worse than having none.
*/

func TestCanonicalSpellingWins(t *testing.T) {
	// Precedence between the two spellings can only be observed where they are
	// distinct variables. On Windows they are the same entry, so the alias is a
	// no-op rather than a fallback — correct either way, but untestable here.
	requireCaseSensitiveEnv(t)
	t.Setenv("SYNCAPP_THING", "canonical")
	t.Setenv("SyncApp_THING", "legacy")

	// Asked for either way, the canonical value is the answer: a deployment
	// mid-migration has both set, and the new one is the one being moved to.
	for _, asked := range []string{"SYNCAPP_THING", "SyncApp_THING"} {
		if got := Get(asked); got != "canonical" {
			t.Errorf("Get(%q) = %q, want the canonical value", asked, got)
		}
	}
}

func TestLegacySpellingStillResolves(t *testing.T) {
	t.Setenv("SyncApp_ONLY_LEGACY", "still-works")

	for _, asked := range []string{"SYNCAPP_ONLY_LEGACY", "SyncApp_ONLY_LEGACY"} {
		if got := Get(asked); got != "still-works" {
			t.Errorf("Get(%q) = %q; an upgrade silently dropped a setting", asked, got)
		}
	}
}

func TestCanonicalOnlyResolves(t *testing.T) {
	t.Setenv("SYNCAPP_ONLY_NEW", "new")

	for _, asked := range []string{"SYNCAPP_ONLY_NEW", "SyncApp_ONLY_NEW"} {
		if got := Get(asked); got != "new" {
			t.Errorf("Get(%q) = %q, want %q", asked, got, "new")
		}
	}
}

// A name carrying neither prefix must be read exactly as given. Rewriting an
// unrelated variable would let this package shadow something it has no business
// touching — PATH, say.
func TestUnprefixedNamesAreUntouched(t *testing.T) {
	t.Setenv("TOTALLY_UNRELATED", "value")
	if got := Get("TOTALLY_UNRELATED"); got != "value" {
		t.Fatalf("Get = %q, want %q", got, "value")
	}

	canonical, legacy := spellings("TOTALLY_UNRELATED")
	if canonical != "TOTALLY_UNRELATED" || legacy != "TOTALLY_UNRELATED" {
		t.Fatalf("spellings rewrote an unprefixed name: %q / %q", canonical, legacy)
	}
}

func TestTypedAccessorsFollowTheAlias(t *testing.T) {
	t.Setenv("SyncApp_A_NUMBER", "42")
	t.Setenv("SyncApp_A_FLOAT", "1.5")
	t.Setenv("SyncApp_A_FLAG", "yes")

	if got := Int("SYNCAPP_A_NUMBER", 0); got != 42 {
		t.Errorf("Int = %d, want 42", got)
	}
	if got := Float("SYNCAPP_A_FLOAT", 0); got != 1.5 {
		t.Errorf("Float = %v, want 1.5", got)
	}
	if !Bool("SYNCAPP_A_FLAG") {
		t.Error("Bool did not follow the alias")
	}
}

// An unparseable value falls back to the default rather than to zero: a typo in
// a tuning knob should leave the documented behaviour in place, not silently
// disable the thing it configures.
func TestTypedAccessorsFallBackOnGarbage(t *testing.T) {
	t.Setenv("SYNCAPP_GARBAGE", "not-a-number")

	if got := Int("SYNCAPP_GARBAGE", 7); got != 7 {
		t.Errorf("Int = %d, want the default 7", got)
	}
	if got := Float("SYNCAPP_GARBAGE", 2.5); got != 2.5 {
		t.Errorf("Float = %v, want the default 2.5", got)
	}
	if Bool("SYNCAPP_GARBAGE") {
		t.Error("Bool treated garbage as true")
	}
}

// Lookup distinguishes "set to empty" from "not set", which Get cannot.
func TestLookupReportsWhetherItWasSet(t *testing.T) {
	t.Setenv("SYNCAPP_SET_EMPTY", "")
	if v, ok := Lookup("SYNCAPP_SET_EMPTY"); !ok || v != "" {
		t.Errorf("Lookup on an empty-but-set variable = %q, %v", v, ok)
	}
	if _, ok := Lookup("SYNCAPP_DEFINITELY_UNSET"); ok {
		t.Error("Lookup reported an unset variable as present")
	}
}
