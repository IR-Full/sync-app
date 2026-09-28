package search

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPostgresSearch exercises the shared tsvector backend. Runs only when
// SYNCAPP_TEST_PG_DSN is set.
func TestPostgresSearch(t *testing.T) {
	dsn := os.Getenv("SYNCAPP_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNCAPP_TEST_PG_DSN to run the Postgres search test")
	}
	ctx := context.Background()
	b, err := NewPostgresBackend(ctx, dsn)
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	// Unique-ish ids to avoid cross-run collisions.
	b.Index(ctx, Doc{MessageID: "9001", ChatID: "7", SenderID: "3", Seq: 1, Text: "lets get pizza tonight", CreatedAt: 1})
	b.Index(ctx, Doc{MessageID: "9002", ChatID: "7", SenderID: "3", Seq: 2, Text: "sushi instead", CreatedAt: 2})

	// The scope is part of the query now. The Postgres backend enforces it with a
	// chat_members subquery, so this test needs user 3 to actually be a member of
	// chat 7 for anything to come back — which is the behaviour under test.
	if err := seedMembership(ctx, dsn, "7", "3"); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	q := Query{Text: "pizza", UserID: "3", Limit: 10}

	hits, err := b.Search(ctx, q)
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
	hits, _ = b.Search(ctx, q)
	for _, h := range hits {
		if h.MessageID == "9001" {
			t.Fatal("deleted doc still searchable")
		}
	}
}

// seedMembership makes the querying user a member of the chat under test.
//
// The scoped query joins chat_members, so without a row there the search
// correctly returns nothing — and a test that did not seed it would "pass" by
// asserting the absence of results it never could have got.
func seedMembership(ctx context.Context, dsn, chatID, userID string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	// The table may not exist if this test runs against a database the main
	// migrations have not touched; create the minimum this query needs.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS chat_members (
		chat_id BIGINT NOT NULL, user_id BIGINT NOT NULL, role TEXT NOT NULL DEFAULT 'member',
		joined_at BIGINT NOT NULL DEFAULT 0, muted BOOLEAN NOT NULL DEFAULT FALSE,
		PRIMARY KEY (chat_id, user_id))`); err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO chat_members (chat_id, user_id, joined_at) VALUES ($1,$2,0)
		 ON CONFLICT DO NOTHING`, chatID, userID)
	return err
}
