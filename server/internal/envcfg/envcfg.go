// Package envcfg reads this server's environment variables.
//
// It exists because the project renamed itself (Synapse → SyncApp) and the
// variable prefix went with it — but call sites and documentation disagreed
// about the spelling, producing the worst kind of configuration bug: setting a
// variable exactly as the README described it did nothing at all, silently,
// with tracing never exporting and the argon2 flood guard quietly falling back
// to its default.
//
// # Which spelling is canonical
//
// SYNCAPP_. Environment variables are conventionally SHOUTING_SNAKE_CASE — POSIX
// reserves that space for them, every shell completion and secrets manager
// assumes it, and a mixed-case name is the kind of thing an operator retypes
// from memory and gets subtly wrong. `SyncApp_FOO` was what the code happened to
// grow, not a decision.
//
// So SYNCAPP_FOO is the documented name and SyncApp_FOO is accepted as a legacy
// alias, forever as far as anyone deploying is concerned: silently dropping a
// setting on upgrade is exactly the failure this package was written to stop.
// A deployment can migrate whenever it likes, or never.
//
// # The rule
//
// One function reads a variable, it understands both spellings, and nothing
// else in the tree calls os.Getenv for a SyncApp/SYNCAPP name. That last part is
// what keeps the alias real: an os.Getenv added later would work for exactly one
// of the two spellings, and which one would depend on who wrote it.
package envcfg

import (
	"os"
	"strconv"
	"strings"
)

// Prefix is the documented spelling: SHOUTING, as environment variables are.
const Prefix = "SYNCAPP_"

// LegacyPrefix is the mixed-case spelling this project used before. Still
// accepted, because a deployment configured against it must not lose its
// settings on upgrade.
const LegacyPrefix = "SyncApp_"

// Get returns the value of a variable, accepting either spelling.
//
// name may be given in either form — call sites read the same as whatever
// documentation they sit next to, and both resolve to the same pair of lookups.
func Get(name string) string {
	canonical, legacy := spellings(name)
	if v := os.Getenv(canonical); v != "" {
		return v
	}
	return os.Getenv(legacy)
}

// Lookup is Get with the "was it set at all" distinction, for the cases where an
// empty value is meaningful rather than absent.
func Lookup(name string) (string, bool) {
	canonical, legacy := spellings(name)
	if v, ok := os.LookupEnv(canonical); ok {
		return v, true
	}
	return os.LookupEnv(legacy)
}

// GetDefault is Get with a fallback for the unset case.
func GetDefault(name, def string) string {
	if v := Get(name); v != "" {
		return v
	}
	return def
}

// Int returns the variable parsed as an int, or def if unset or unparseable.
func Int(name string, def int) int {
	v := Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// Float returns the variable parsed as a float64, or def if unset/unparseable.
func Float(name string, def float64) float64 {
	v := Get(name)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// Bool reports whether the variable is set to a truthy value. "1", "true" and
// "yes" all count, because all three appear in this project's own docs.
func Bool(name string) bool {
	switch strings.ToLower(Get(name)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// spellings returns the canonical and legacy forms of a name, whichever it was
// given in. A name carrying neither prefix is returned unchanged in both slots,
// so an unrelated variable is read exactly once and never shadowed.
func spellings(name string) (canonical, legacy string) {
	switch {
	case strings.HasPrefix(name, Prefix):
		return name, LegacyPrefix + name[len(Prefix):]
	case strings.HasPrefix(name, LegacyPrefix):
		return Prefix + name[len(LegacyPrefix):], name
	default:
		return name, name
	}
}
