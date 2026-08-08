package platform

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/store/sharded"
)

// Sharding is switched on by an environment variable, and everything downstream
// — which store the daemons write through, how many outboxes a relay must drain,
// which optional capabilities survive — follows from what this function builds.
// That makes Load the seam where a misconfiguration turns into silently missing
// behaviour, so it is worth asserting on directly rather than inferring from a
// running fleet.
//
// Runs only when SYNAPSE_TEST_SHARD_DSNS is set to two or more DSNs.
func TestLoadBuildsAShardedStoreWithAnOutboxPerShard(t *testing.T) {
	dsns := os.Getenv("SYNAPSE_TEST_SHARD_DSNS")
	if dsns == "" || len(strings.Split(dsns, ",")) < 2 {
		t.Skip("set SYNAPSE_TEST_SHARD_DSNS (2+ comma-separated DSNs) to run the sharded platform test")
	}
	want := len(strings.Split(dsns, ","))

	// Load reads the environment; point the primary at the first shard so the run
	// needs nothing beyond the shards themselves.
	first := strings.TrimSpace(strings.Split(dsns, ",")[0])
	t.Setenv("SYNAPSE_PG_DSN", first)
	t.Setenv("SYNAPSE_MESSAGE_SHARD_DSNS", dsns)

	b, err := Load(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer b.Close()

	sh, ok := b.MessageStore.(*sharded.MessageStore)
	if !ok {
		t.Fatalf("message store is %T, not sharded — the environment variable did nothing", b.MessageStore)
	}
	if got := len(sh.Shards()); got != want {
		t.Fatalf("built %d shards from %d DSNs", got, want)
	}

	// A relay drains outboxes, and each shard stages its own: one entry short here
	// means the events of a whole shard are never published, which looks like
	// "some chats stopped delivering" and nothing else.
	if got := len(b.MsgOutbox); got != want {
		t.Fatalf("relay would drain %d outboxes for %d shards", got, want)
	}

	// The optional capabilities must survive the decorator — this is the failure
	// that had no symptom: no error, no log, just a feature that stopped existing
	// for whoever listed more than one DSN.
	if _, ok := b.MessageStore.(store.Expirer); !ok {
		t.Fatal("the sharded store is not an Expirer: self-destruct would silently never run")
	}
	if _, ok := b.MessageStore.(store.ThreadReader); !ok {
		t.Fatal("the sharded store is not a ThreadReader: threads would silently return nothing")
	}
	if _, ok := b.MessageStore.(store.MediaReferencer); !ok {
		t.Fatal("the sharded store cannot answer media reference checks: the sweep would be disabled")
	}
}
