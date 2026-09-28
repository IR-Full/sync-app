package billing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func yooWithDefaultSources(t *testing.T, api *fakeYooAPI) *YooKassa {
	t.Helper()
	y := newYoo(t, api, "")
	sources, err := ParseSources(YooKassaNotificationSources)
	if err != nil {
		t.Fatal(err)
	}
	y.AllowedSources = sources
	return y
}

func postNotification(h *Handler, remote string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/billing/webhook/"+ProviderYooKassa,
		strings.NewReader(string(notification("2d1f-aa", "succeeded", "299.00"))))
	req.RemoteAddr = remote
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A notification from outside YooKassa's published ranges is refused before it
// costs an API lookup; one from inside goes on to the lookup as before.
func TestYooKassaRefusesNotificationsFromOutsideItsRanges(t *testing.T) {
	api := &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}
	svc, _, _ := newSvc(t, yooWithDefaultSources(t, api))
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if rec := postNotification(h, "203.0.113.9:4431", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("outside the ranges: HTTP %d, want 403", rec.Code)
	}
	if n := api.gets.Load(); n != 0 {
		t.Fatalf("a refused notification still made %d API lookups", n)
	}

	for _, remote := range []string{"185.71.76.5:443", "77.75.156.11:443", "[2a02:5180::1]:443", "[::ffff:185.71.77.20]:443"} {
		if rec := postNotification(h, remote, nil); rec.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d, want 200", remote, rec.Code)
		}
	}
	if n := api.gets.Load(); n != 4 {
		t.Fatalf("API lookups = %d, want 4 (one per accepted notification)", n)
	}
}

// Behind a proxy the peer is the proxy. The handler asks the resolver it was
// given (the gateway's, which honours X-Forwarded-For only from trusted hops),
// so the check sees YooKassa's address, and a forged header from anyone else
// changes nothing.
func TestYooKassaSourceIsResolvedThroughTheConfiguredResolver(t *testing.T) {
	api := &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}
	svc, _, _ := newSvc(t, yooWithDefaultSources(t, api))
	proxy := "10.0.0.2"
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithClientIP(func(r *http.Request) string {
			if peerIP(r) == proxy {
				return r.Header.Get("X-Forwarded-For")
			}
			return peerIP(r)
		})
	fwd := http.Header{"X-Forwarded-For": {"185.71.76.5"}}

	if rec := postNotification(h, proxy+":5000", fwd); rec.Code != http.StatusOK {
		t.Fatalf("via the trusted proxy: HTTP %d, want 200", rec.Code)
	}
	if rec := postNotification(h, "203.0.113.9:5000", fwd); rec.Code != http.StatusForbidden {
		t.Fatalf("forged header from a stranger: HTTP %d, want 403", rec.Code)
	}
}

// A caller that records no source cannot get past a restricted provider: an
// unknown address is not an allowed one.
func TestRestrictedProviderRefusesAnUnknownSource(t *testing.T) {
	api := &fakeYooAPI{status: "succeeded", paid: "true", amount: "299.00"}
	svc, _, _ := newSvc(t, yooWithDefaultSources(t, api))
	err := svc.HandleCallback(context.Background(), ProviderYooKassa, notification("2d1f-aa", "succeeded", "299.00"), nil)
	if !errors.Is(err, ErrUntrustedSource) {
		t.Fatalf("err = %v, want ErrUntrustedSource", err)
	}
}

// Without a list there is no address check — the deployment opted out.
func TestUnrestrictedYooKassaAcceptsAnySource(t *testing.T) {
	y := &YooKassa{}
	if !y.AllowsSource(netip.MustParseAddr("203.0.113.9")) || !y.AllowsSource(netip.Addr{}) {
		t.Fatal("an empty AllowedSources must not restrict anything")
	}
}

func TestParseSources(t *testing.T) {
	got, err := ParseSources([]string{" 192.0.2.7 ", "", "10.1.2.3/8", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.0.2.7/32", "10.0.0.0/8", "2001:db8::1/128"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("entry %d = %s, want %s", i, got[i], want[i])
		}
	}
	if _, err := ParseSources([]string{"185.71.76.0/27", "185.71.76.0/33"}); err == nil {
		t.Fatal("a bad entry must fail the whole list, not be dropped")
	}
	if _, err := ParseSources(YooKassaNotificationSources); err != nil {
		t.Fatalf("the built-in list does not parse: %v", err)
	}
}
