package billing

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Shared provider plumbing.

const (
	// providerTimeout bounds an outbound acquirer call. Short, because it sits in a
	// user-facing checkout: a client waiting on a payment page would rather be told
	// to retry than watch a spinner for a minute.
	providerTimeout = 15 * time.Second
	// maxProviderResponse bounds what is read back. An acquirer will not send
	// megabytes, and an endpoint that does is either misconfigured or not the
	// acquirer.
	maxProviderResponse = 1 << 20
	// webhookTolerance is how far a signed callback timestamp may be from now.
	//
	// It exists because a signature with no time bound is valid forever, so a
	// callback captured once could be replayed at any point in the future. Five
	// minutes is the usual window: wide enough for clock skew and a retry, narrow
	// enough that a captured body stops being useful quickly.
	webhookTolerance = 5 * time.Minute
	// maxWebhookBody bounds a callback body.
	//
	// The endpoint needs no account to reach, so this is not a tidiness limit: an
	// unbounded read there is a memory-exhaustion primitive available to anyone who
	// knows the URL. A notification is a few kilobytes of JSON.
	maxWebhookBody = 1 << 20
)

// headerValue looks a header up case-insensitively.
//
// Necessary rather than fastidious: HTTP header names are case-insensitive, Go
// canonicalises them in one direction and gRPC metadata in another, and a webhook
// handler that reads a map built by something else finds "X-Signature" or
// "x-signature" depending on where the map came from. Missing the signature because
// of capitalisation would fail closed, which is safe but very confusing.
func headerValue(headers map[string]string, name string) string {
	if v, ok := headers[name]; ok {
		return v
	}
	canonical := http.CanonicalHeaderKey(name)
	if v, ok := headers[canonical]; ok {
		return v
	}
	lower := strings.ToLower(name)
	for k, v := range headers {
		if strings.ToLower(k) == lower {
			return v
		}
	}
	return ""
}

// minorToDecimal renders integer minor units as the decimal string acquirers want.
//
// Assembled from the integer, never formatted from a float. `fmt.Sprintf("%.2f",
// float64(minor)/100)` looks equivalent and is not: 0.01 has no exact binary
// representation, so the conversion introduces an error before the formatting
// rounds it, and the amount that arrives is occasionally not the amount intended.
func minorToDecimal(minor int64) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	whole := minor / 100
	cents := minor % 100
	out := strconv.FormatInt(whole, 10) + "." + fmt.Sprintf("%02d", cents)
	if neg {
		return "-" + out
	}
	return out
}

// decimalToMinor parses an acquirer decimal string back to integer minor units.
//
// Parsed by SPLITTING on the point rather than through ParseFloat, for the same
// reason as above: a float round-trip can turn "29900.00" kopeks worth of value
// into something one minor unit away, and a callback amount that differs by one is
// rejected as a mismatch.
func decimalToMinor(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty amount")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, found := strings.Cut(s, ".")
	if !found {
		frac = "0"
	}
	// Pad or trim to exactly two digits. A provider sending "10.5" means ten fifty,
	// not ten and five kopeks, and one sending "10.500" is still ten fifty.
	switch {
	case len(frac) == 1:
		frac += "0"
	case len(frac) > 2:
		frac = frac[:2]
	case len(frac) == 0:
		frac = "00"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	out := w*100 + f
	if neg {
		out = -out
	}
	return out, nil
}

// truncate bounds an error message built from a provider body.
//
// Acquirer errors are the only thing that explains a refused charge, so they are
// included — but a body that turns out to be a megabyte of HTML must not become a
// megabyte of log line.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
