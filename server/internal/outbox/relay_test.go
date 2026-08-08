package outbox

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/eventbus"
)

// purgeStore records what the janitor asked for and can pretend to hold a
// backlog, so we can assert the relay keeps deleting in chunks until it catches
// up instead of stopping after one batch.
type purgeStore struct {
	mu        sync.Mutex
	backlog   int
	calls     int
	lastBefor int64
	lastLimit int
}

func (p *purgeStore) Poll(context.Context, int) ([]store.OutboxRecord, error) { return nil, nil }
func (p *purgeStore) MarkSent(context.Context, []string) error                { return nil }

func (p *purgeStore) PurgeSent(_ context.Context, before int64, limit int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.lastBefor, p.lastLimit = before, limit
	n := limit
	if p.backlog < n {
		n = p.backlog
	}
	p.backlog -= n
	return n, nil
}

func (p *purgeStore) snapshot() (calls, backlog int, before int64, limit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.backlog, p.lastBefor, p.lastLimit
}

// TestRelayCollectsPublishedRows pins the janitor. The outbox stages a full copy
// of every message, so a relay that only ever marks rows sent turns the handoff
// table into a permanent second message log — the failure is invisible in tests
// and obvious on a disk graph six months later.
func TestRelayCollectsPublishedRows(t *testing.T) {
	st := &purgeStore{backlog: 2500} // more than one batch
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := New(st, eventbus.NewMemory(), log).WithRetention(time.Minute, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, backlog, _, _ := st.snapshot(); backlog == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	calls, backlog, before, limit := st.snapshot()
	if backlog != 0 {
		t.Fatalf("janitor left %d rows behind after %d calls", backlog, calls)
	}
	if limit != defaultPurgeBatch {
		t.Fatalf("purge batch %d, want %d", limit, defaultPurgeBatch)
	}
	// Only PUBLISHED-and-aged rows may go: the cutoff must be in the past.
	if before >= time.Now().UnixMilli() {
		t.Fatalf("cutoff %d is not in the past — the janitor would delete rows the relay has not published yet", before)
	}
}
