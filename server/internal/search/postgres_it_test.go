package search

import (
	"context"
	"os"
	"testing"
)

// TestPostgresSearch exercises the shared tsvector backend. Runs only when
// SYNAPSE_TEST_PG_DSN is set.
func TestPostgresSearch(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the Postgres search test")
	}
	ctx := context.Background()
	b, err := NewPostgresBackend(ctx, dsn)
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	// Unique-ish ids to avoid cross-run collisions.
	b.Index(ctx, Doc{MessageID: "9001", ChatID: "7", SenderID: "3", Seq: 1, Text: "lets get pizza tonight"})
	b.Index(ctx, Doc{MessageID: "9002", ChatID: "7", SenderID: "3", Seq: 2, Text: "sushi instead"})

	hits, err := b.Search(ctx, "pizza", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		if h.MessageID == "9001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pizza not found; hits=%+v", hits)
	}

	// Delete removes it from the index.
	b.Delete(ctx, "9001")
	hits, _ = b.Search(ctx, "pizza", 10)
	for _, h := range hits {
		if h.MessageID == "9001" {
			t.Fatal("deleted doc still searchable")
		}
	}
}
