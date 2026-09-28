package platform

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
)

/*
The production preflight.

`SYNCAPP_REQUIRE_TLS=1` is documented as the switch that says "this is
production", and it used to enforce exactly one thing: that TLS was configured.
Everything else that is unsafe to ship stayed a `log.Warn` — the default media
signing secret, an empty WebSocket origin allow-list, an unset per-IP accept
guard. A warning does not stop a deploy. Anyone who has watched a startup log
scroll past knows it does not even get read.

So the switch now means what it says. Every check below is something that is
fine in development and is a real vulnerability in production:

  - the default media secret is a constant in this repository, so whoever knows
    it can mint valid upload and download URLs for any blob;
  - an empty origin allow-list makes `CheckOrigin` return true for every origin,
    which is cross-site WebSocket hijacking;
  - with no per-IP caps the accept guard is never constructed at all, so
    connection floods reach the handshake.

All violations are collected and reported together rather than one at a time.
Failing on the first would have an operator fix it, redeploy, wait, and discover
the next one — three deploys to learn what one message could have said.

This lives in platform rather than in cmd/server, and that is the point of it
being here. The checks describe the CLIENT EDGE — the thing that terminates
WebSocket upgrades and signs media URLs — and there are two binaries that are
that edge: the monolith and `gatewayd`. While the preflight sat in
`package main` next to the monolith, the split deployment, which is the one that
actually gets rolled out at scale, booted with the repository's development
media secret and an allow-list that accepted every origin, and said nothing.
A safety check that only covers the deployment nobody ships is decoration.
*/

// CheckProduction returns every production-policy violation in the environment.
// Empty means the configuration is safe to ship.
func CheckProduction() []string {
	var problems []string

	// TLS. The listener config is built later (it can generate a certificate), so
	// this checks the same environment BuildTLSConfig reads. The late check
	// against the built config stays as well — this one exists to fail fast and
	// alongside its siblings.
	if envcfg.Get("SYNCAPP_TLS_CERT") == "" || envcfg.Get("SYNCAPP_TLS_KEY") == "" {
		if envcfg.Get("SYNCAPP_TLS_SELFSIGNED") != "1" {
			problems = append(problems,
				"SYNCAPP_TLS_CERT and SYNCAPP_TLS_KEY are unset: traffic would be plaintext")
		} else {
			problems = append(problems,
				"SYNCAPP_TLS_SELFSIGNED=1 is a development certificate: no client can verify it, "+
					"so every connection is either refused or trusting an unauthenticated peer")
		}
	}

	if envcfg.Get("SYNCAPP_MEDIA_SECRET") == "" {
		problems = append(problems,
			"SYNCAPP_MEDIA_SECRET is unset: signed media URLs would be minted with a constant "+
				"compiled into this repository, so anyone can forge upload and download links")
	} else if MediaSecretIsDefault() {
		problems = append(problems,
			"SYNCAPP_MEDIA_SECRET is set to the development default: change it")
	}

	if strings.TrimSpace(envcfg.Get("SYNCAPP_ALLOWED_ORIGINS")) == "" {
		problems = append(problems,
			"SYNCAPP_ALLOWED_ORIGINS is unset: the WebSocket upgrade would accept every origin "+
				"(cross-site WebSocket hijacking). Set your web origins, comma-separated")
	}

	if !positiveInt("SYNCAPP_MAX_CONNS_PER_IP") {
		problems = append(problems,
			"SYNCAPP_MAX_CONNS_PER_IP is unset or not positive: the per-IP accept guard is not built, "+
				"so one source can hold unlimited connections")
	}
	if !positiveFloat("SYNCAPP_ACCEPT_RATE_PER_IP") {
		problems = append(problems,
			"SYNCAPP_ACCEPT_RATE_PER_IP is unset or not positive: connection floods are accepted "+
				"at line rate and reach the handshake")
	}

	return problems
}

// EnforceProduction fails the boot when SYNCAPP_REQUIRE_TLS=1 and anything is
// misconfigured. Outside production it reports the same list as warnings, so a
// developer sees exactly what would block a real deployment before they try one.
func EnforceProduction(warn func(msg string, args ...any)) error {
	problems := CheckProduction()
	if len(problems) == 0 {
		return nil
	}
	if !RequireTLS() {
		for _, p := range problems {
			warn("not production-ready: " + p)
		}
		warn("the settings above are development defaults; SYNCAPP_REQUIRE_TLS=1 refuses to start without them")
		return nil
	}
	var b strings.Builder
	b.WriteString("SYNCAPP_REQUIRE_TLS=1 declares production, but the configuration is not safe to ship:\n")
	for _, p := range problems {
		b.WriteString("  - ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	return fmt.Errorf("%s", b.String())
}

// RequireTLS reports whether the deployment declared itself production via the
// mandatory-TLS policy switch. Reused as the "this is production" signal so there
// is one flag to set rather than two that can disagree.
//
// Exported and single because it was neither: the monolith and platform each had
// a private copy that accepted "1"/"true"/"yes", while gatewayd compared against
// the literal "1". So SYNCAPP_REQUIRE_TLS=true switched production on for the
// node-id check and off for TLS enforcement in the same process — a disagreement
// about which environment this is, which is the one thing this flag exists to
// settle.
func RequireTLS() bool {
	switch strings.ToLower(envcfg.Get("SYNCAPP_REQUIRE_TLS")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func positiveInt(key string) bool {
	n, err := strconv.Atoi(envcfg.Get(key))
	return err == nil && n > 0
}

func positiveFloat(key string) bool {
	f, err := strconv.ParseFloat(envcfg.Get(key), 64)
	return err == nil && f > 0
}
