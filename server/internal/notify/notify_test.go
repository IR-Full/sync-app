package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/pkg/eventbus"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestProviderRetriesOnlyWhatRetryingCanFix pins the classification. A timeout or
// a 5xx is a different kind of failure from a rejected payload: the first is
// worth another attempt, the second is the same failure repeated at the
// provider's expense and ours.
func TestProviderRetriesOnlyWhatRetryingCanFix(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewHTTPProvider(srv.URL, "k", discard())
	p.BaseDelay = time.Millisecond
	if err := p.Send(context.Background(), PushJob{UserID: "1", Preview: "hi", Token: "t"}); err != nil {
		t.Fatalf("transient failures were not retried through: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("made %d attempts, want 3", got)
	}

	// A 400 is the provider saying "this will never work". One attempt only.
	var rejects atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rejects.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer bad.Close()
	p2 := NewHTTPProvider(bad.URL, "k", discard())
	p2.BaseDelay = time.Millisecond
	if err := p2.Send(context.Background(), PushJob{UserID: "1", Token: "t"}); err == nil {
		t.Fatal("a rejected payload was reported as delivered")
	}
	if got := rejects.Load(); got != 1 {
		t.Fatalf("retried an unretryable rejection %d times", got-1)
	}
}

// fakeDevices is a user's device list plus a record of which tokens were dropped.
type fakeDevices struct {
	mu      sync.Mutex
	devs    []DeviceToken
	dropped []string
}

func (f *fakeDevices) ListDevices(context.Context, string) ([]DeviceToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]DeviceToken(nil), f.devs...), nil
}

func (f *fakeDevices) InvalidateToken(_ context.Context, deviceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, deviceID)
	return nil
}

// TestPushFansOutPerDeviceAndDropsDeadTokens pins the two properties that make
// the offline path survive contact with real users: a user is a SET of devices,
// and a device that uninstalled the app must stop costing a delivery attempt on
// every message they are ever sent again.
func TestPushFansOutPerDeviceAndDropsDeadTokens(t *testing.T) {
	var delivered atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p pushPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p.Token == "dead" {
			w.WriteHeader(http.StatusGone) // APNs 410 / FCM unregistered
			return
		}
		delivered.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	devs := &fakeDevices{devs: []DeviceToken{
		{DeviceID: "d1", Platform: "ios", Token: "live-1"},
		{DeviceID: "d2", Platform: "android", Token: "dead"},
		{DeviceID: "d3", Platform: "web", Token: ""}, // registered, no token yet
		{DeviceID: "d4", Platform: "ios", Token: "live-2"},
	}}
	p := NewHTTPProvider(srv.URL, "", discard())
	p.BaseDelay = time.Millisecond
	svc := New(eventbus.NewMemory(), p, discard()).WithDevices(devs)

	job := PushJob{UserID: "u1", ChatID: "c1", MessageID: "m1", Preview: "hello"}
	data, _ := json.Marshal(job)
	if err := svc.onPush(context.Background(), eventbus.Event{Data: data}); err != nil {
		t.Fatalf("a dead token failed the whole job: %v", err)
	}

	if got := delivered.Load(); got != 2 {
		t.Fatalf("delivered to %d devices, want the 2 live ones", got)
	}
	devs.mu.Lock()
	dropped := append([]string(nil), devs.dropped...)
	devs.mu.Unlock()
	if len(dropped) != 1 || dropped[0] != "d2" {
		t.Fatalf("dead token cleanup dropped %v, want [d2]", dropped)
	}
}
