package billing

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
)

/*
Where a callback came from.

An acquirer that publishes the addresses it sends notifications from gives the
webhook a cheap first gate: a notification from anywhere else is refused before
it costs an API lookup. It is a SECOND layer and never the only one — an address
is not a signature, and YooKassa's own authenticity check stays the payment
fetched back from its API (YooKassa.Verify). What the gate adds is that a
stranger who knows the webhook URL can no longer make this server call the
acquirer's API on demand.

The address has to be the acquirer's, not the last hop's. Behind an ingress the
peer is the proxy, so the handler resolves the forwarded address the same way
the gateway's connection guard does — trusting X-Forwarded-For only from a
configured proxy (SYNCAPP_TRUSTED_PROXIES). An operator who puts the server
behind a proxy and does not configure it will see every notification refused
with 403 and a log line saying why; the acquirer keeps retrying meanwhile, so
the fix does not lose payments.
*/

type callbackSourceKey struct{}

// WithCallbackSource records the address a callback arrived from, for providers
// that restrict where their notifications may come from (SourceRestricted).
func WithCallbackSource(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, callbackSourceKey{}, addr)
}

// callbackSource returns the recorded source, or the zero Addr when none was
// recorded or it does not parse. A restricted provider refuses the zero Addr.
func callbackSource(ctx context.Context) netip.Addr {
	s, _ := ctx.Value(callbackSourceKey{}).(string)
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

// SourceRestricted is implemented by a provider that knows where its
// notifications come from. HandleCallback asks it before Verify.
type SourceRestricted interface {
	AllowsSource(src netip.Addr) bool
}

// YooKassaNotificationSources are the addresses YooKassa publishes for its HTTP
// notifications ("Входящие уведомления" in its API documentation). They change
// rarely, but they do change: SYNCAPP_YOOKASSA_ALLOWED_IPS overrides the list
// without a release.
var YooKassaNotificationSources = []string{
	"185.71.76.0/27",
	"185.71.77.0/27",
	"77.75.153.0/25",
	"77.75.156.11",
	"77.75.156.35",
	"77.75.154.128/25",
	"2a02:5180::/32",
}

// ParseSources turns CIDRs ("10.0.0.0/8") and bare addresses ("192.0.2.7") into
// prefixes. Any bad entry fails the whole list: a typo that silently dropped a
// range would refuse that range's notifications with nothing to explain why.
func ParseSources(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if p, err := netip.ParsePrefix(entry); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("billing: %q is neither an address nor a CIDR", entry)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
