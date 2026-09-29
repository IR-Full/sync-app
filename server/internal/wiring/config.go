// Package wiring assembles the service graph. It is the one place that decides
// which service gets which dependency, so the monolith (cmd/server) and the
// split deployment (cmd/gatewayd plus the service daemons) cannot wire the same
// service differently.
//
// The two topologies differ only in Core: in-process services in the monolith,
// gRPC clients at the edge of the split. Everything else is built here, from the
// same Config, by the same functions.
package wiring

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/SyncApp-chat/SyncApp/internal/billing"
	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
	"github.com/SyncApp-chat/SyncApp/internal/gateway"
)

// Config is every setting the service graph reads from the environment, read
// once. Both topologies build it with FromEnv, so a variable cannot be honoured
// by one and ignored by the other.
type Config struct {
	TCPAddr   string
	WSAddr    string
	PublicURL string

	MediaDir      string
	MediaMaxBytes int64

	// SearchDSN selects the shared Postgres search index; empty means in-memory.
	SearchDSN string

	PushEndpoint string
	PushKey      string

	BannedTerms []string

	QUIC  bool
	PProf bool
	// ProxyProtocol reads a PROXY v2 header on raw-TCP connections from a trusted
	// proxy, which is how a TCP balancer passes on the client address.
	ProxyProtocol bool

	// Gateway is the edge configuration without NodeID, which belongs to the
	// process rather than to the environment (see GatewayConfig).
	Gateway gateway.Config

	// YooKassa and Stripe are nil when their credentials are not configured.
	YooKassa *billing.YooKassa
	Stripe   *billing.Stripe
}

// FromEnv reads Config from the environment. A value that is set but cannot be
// parsed is an error rather than a silent fallback to the default: a typo in
// SYNCAPP_MAX_CONNS_PER_IP must not quietly switch the per-IP guard off.
func FromEnv() (Config, error) {
	var errs []error
	cfg := Config{
		TCPAddr:       envcfg.GetDefault("SYNCAPP_TCP_ADDR", ":7000"),
		WSAddr:        envcfg.GetDefault("SYNCAPP_WS_ADDR", ":8080"),
		MediaDir:      envcfg.GetDefault("SYNCAPP_MEDIA_DIR", "./data/media"),
		SearchDSN:     envcfg.Get("SYNCAPP_PG_DSN"),
		PushEndpoint:  envcfg.Get("SYNCAPP_PUSH_ENDPOINT"),
		PushKey:       envcfg.Get("SYNCAPP_PUSH_KEY"),
		BannedTerms:   splitList(envcfg.GetDefault("SYNCAPP_BANNED_TERMS", "spamword,scamlink")),
		QUIC:          envcfg.Bool("SYNCAPP_QUIC"),
		PProf:         envcfg.Bool("SYNCAPP_PPROF"),
		ProxyProtocol: envcfg.Bool("SYNCAPP_PROXY_PROTOCOL"),
		Gateway:       gateway.DefaultConfig(),
	}
	cfg.PublicURL = envcfg.GetDefault("SYNCAPP_PUBLIC_URL", "http://localhost"+cfg.WSAddr)

	if n, ok, err := intVar("SYNCAPP_MEDIA_MAX_BYTES"); err != nil {
		errs = append(errs, err)
	} else if ok {
		cfg.MediaMaxBytes = int64(n)
	}

	g := &cfg.Gateway
	if f, ok, err := floatVar("SYNCAPP_SEND_RATE"); err != nil {
		errs = append(errs, err)
	} else if ok {
		g.SendRate, g.SendBurst = f, f*2
	}
	if n, ok, err := intVar("SYNCAPP_MAX_CONNS_PER_IP"); err != nil {
		errs = append(errs, err)
	} else if ok {
		g.MaxConnsPerIP = n
	}
	if f, ok, err := floatVar("SYNCAPP_ACCEPT_RATE_PER_IP"); err != nil {
		errs = append(errs, err)
	} else if ok {
		g.AcceptRatePerIP = f
	}
	g.AllowedOrigins = splitList(envcfg.Get("SYNCAPP_ALLOWED_ORIGINS"))
	// Without this behind an ingress, the per-IP guard sees one address for every
	// client and the production preflight's mandatory per-IP cap refuses them all.
	g.TrustedProxies = splitList(envcfg.Get("SYNCAPP_TRUSTED_PROXIES"))
	if bad := invalidProxies(g.TrustedProxies); len(bad) > 0 {
		errs = append(errs, fmt.Errorf("SYNCAPP_TRUSTED_PROXIES: not an address or CIDR: %s", strings.Join(bad, ", ")))
	}
	if cfg.ProxyProtocol && len(g.TrustedProxies) == 0 {
		errs = append(errs, errors.New("SYNCAPP_PROXY_PROTOCOL needs SYNCAPP_TRUSTED_PROXIES: the header is read only from a trusted proxy"))
	}
	g.AdminUsers = splitList(envcfg.Get("SYNCAPP_ADMIN_USERS"))
	g.ModeratorUsers = splitList(envcfg.Get("SYNCAPP_MODERATOR_USERS"))

	if shop := envcfg.Get("SYNCAPP_YOOKASSA_SHOP_ID"); shop != "" {
		sources, err := yooKassaSources(envcfg.Get("SYNCAPP_YOOKASSA_ALLOWED_IPS"))
		if err != nil {
			errs = append(errs, err)
		}
		cfg.YooKassa = &billing.YooKassa{
			Endpoint:  envcfg.GetDefault("SYNCAPP_YOOKASSA_ENDPOINT", "https://api.yookassa.ru/v3/payments"),
			ShopID:    shop,
			SecretKey: envcfg.Get("SYNCAPP_YOOKASSA_SECRET"),
			// Only for notifications relayed through a signing proxy. YooKassa does not
			// sign its own; each one is checked by fetching the payment back from the
			// API (billing.YooKassa.Verify).
			WebhookSecret:  envcfg.Get("SYNCAPP_YOOKASSA_WEBHOOK_SECRET"),
			AllowedSources: sources,
		}
	}
	if key := envcfg.Get("SYNCAPP_STRIPE_SECRET"); key != "" {
		cfg.Stripe = &billing.Stripe{
			Endpoint:      envcfg.GetDefault("SYNCAPP_STRIPE_ENDPOINT", "https://api.stripe.com/v1/payment_intents"),
			SecretKey:     key,
			WebhookSecret: envcfg.Get("SYNCAPP_STRIPE_WEBHOOK_SECRET"),
		}
	}
	return cfg, errors.Join(errs...)
}

// GatewayConfig is the edge configuration for the node that runs it.
func (c Config) GatewayConfig(nodeID int64) gateway.Config {
	g := c.Gateway
	g.NodeID = strconv.FormatInt(nodeID, 10)
	return g
}

// yooKassaSources reads SYNCAPP_YOOKASSA_ALLOWED_IPS: unset means YooKassa's
// published notification addresses, "off" means no address check (nil), anything
// else is a comma-separated list of addresses and CIDRs that replaces them. A bad
// entry stops startup rather than quietly narrowing the list.
func yooKassaSources(v string) ([]netip.Prefix, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "":
		return billing.ParseSources(billing.YooKassaNotificationSources)
	case "off":
		return nil, nil
	}
	sources, err := billing.ParseSources(strings.Split(v, ","))
	if err == nil && len(sources) == 0 {
		err = errors.New("SYNCAPP_YOOKASSA_ALLOWED_IPS lists no addresses; use \"off\" to disable the check")
	}
	return sources, err
}

// invalidProxies returns the entries that are neither an address nor a CIDR.
func invalidProxies(entries []string) []string {
	var bad []string
	for _, e := range entries {
		if _, err := netip.ParsePrefix(e); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(e); err == nil {
			continue
		}
		bad = append(bad, e)
	}
	return bad
}

// splitList splits a comma-separated variable, dropping blanks and whitespace.
// Unset yields nil, so "not configured" and "configured empty" read the same.
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func intVar(name string) (int, bool, error) {
	v := envcfg.Get(name)
	if v == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("%s=%q: want a non-negative integer", name, v)
	}
	return n, true, nil
}

func floatVar(name string) (float64, bool, error) {
	v := envcfg.Get(name)
	if v == "" {
		return 0, false, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 {
		return 0, false, fmt.Errorf("%s=%q: want a non-negative number", name, v)
	}
	return f, true, nil
}
