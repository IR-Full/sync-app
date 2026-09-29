package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/internal/store/storetest"
)

// The same contract suite the in-memory store runs, against real Postgres.
//
// This is the test that gives the "swap the message store later" claim in
// ARCHITECTURE.md §8 any weight: the two backends are only interchangeable if
// they agree on the behaviour nothing in the type system pins down — who
// allocates a sequence, whether a duplicate consumes one, whether a capped
// invite can be over-redeemed by concurrent joins.
//
// Skipped without SYNCAPP_TEST_PG_DSN, like the other integration tests here:
//
//	docker compose up -d postgres
//	SYNCAPP_TEST_PG_DSN="postgres://syncapp:syncapp@127.0.0.1:5432/syncapp?sslmode=disable" \
//	  go test ./internal/store/postgres/ -run TestConformance
func TestConformance(t *testing.T) {
	dsn := os.Getenv("SYNCAPP_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNCAPP_TEST_PG_DSN to run the Postgres conformance suite")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// One connection, shared by every subtest. The suite hands out unique ids per
	// case rather than truncating tables, so subtests stay isolated without
	// needing a database each — and without a shared-state trap, since nothing
	// asserts on global counts.
	stores := st.Stores()
	storetest.Run(t, func(t *testing.T) store.Stores { return stores })
}
