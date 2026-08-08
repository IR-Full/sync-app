package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/id"
)

// TestPostgresRoundTrip exercises the durable store against a real Postgres. It
// runs only when SYNAPSE_TEST_PG_DSN is set (so `go test ./...` stays green
// without Docker), e.g.:
//
//	SYNAPSE_TEST_PG_DSN="postgres://synapse:synapse@localhost:5432/synapse?sslmode=disable" \
//	  go test ./internal/store/postgres/ -run TestPostgres
//
// Bring the DB up first with `docker compose up -d postgres`.
func TestPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the Postgres integration test")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ids, _ := id.NewGenerator(7)
	now := time.Now().UnixMilli()

	// User + group chat with the owner as a member.
	u := &model.User{ID: ids.NextString(), Username: "it_" + ids.NextString(), PasswordHash: "x", CreatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	ch := &model.Chat{ID: ids.NextString(), Type: model.ChatGroup, OwnerID: u.ID, CreatedAt: now}
	if err := st.CreateChat(ctx, ch); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := st.AddMember(ctx, &model.ChatMember{ChatID: ch.ID, UserID: u.ID, Role: model.RoleOwner, JoinedAt: now}); err != nil {
		t.Fatalf("add member: %v", err)
	}

	mkOb := func(m *model.Message) *store.OutboxRecord {
		return &store.OutboxRecord{ID: ids.NextString(), Subject: "message.created", Key: m.ChatID, Data: []byte(`{"ok":true}`)}
	}

	// Insert allocates seq atomically and stages an outbox event.
	m := &model.Message{ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "hi", CreatedAt: now}
	stored, dup, err := st.InsertMessage(ctx, m, "dedupA", mkOb)
	if err != nil || dup || stored.Seq != 1 {
		t.Fatalf("insert: seq=%d dup=%v err=%v", stored.Seq, dup, err)
	}

	// Same dedup key → duplicate, same id, no new seq consumed.
	m2 := &model.Message{ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "hi", CreatedAt: now}
	stored2, dup2, err := st.InsertMessage(ctx, m2, "dedupA", mkOb)
	if err != nil {
		t.Fatalf("dedup insert: %v", err)
	}
	if !dup2 || stored2.ID != stored.ID {
		t.Fatalf("dedup: dup=%v id=%s vs %s", dup2, stored2.ID, stored.ID)
	}

	// A second distinct message gets seq 2 (gap-free).
	m3 := &model.Message{ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "yo", CreatedAt: now}
	stored3, _, err := st.InsertMessage(ctx, m3, "dedupB", mkOb)
	if err != nil || stored3.Seq != 2 {
		t.Fatalf("second insert seq=%d err=%v", stored3.Seq, err)
	}

	// The outbox holds the staged events; drain and mark sent.
	recs, err := st.Poll(ctx, 100)
	if err != nil || len(recs) < 2 {
		t.Fatalf("poll: n=%d err=%v", len(recs), err)
	}
	ids2 := make([]string, len(recs))
	for i, r := range recs {
		ids2[i] = r.ID
	}
	if err := st.MarkSent(ctx, ids2); err != nil {
		t.Fatalf("marksent: %v", err)
	}
	after, err := st.Poll(ctx, 100)
	if err != nil {
		t.Fatalf("poll2: %v", err)
	}
	for _, r := range after {
		for _, sentID := range ids2 {
			if r.ID == sentID {
				t.Fatalf("record %s still unsent after MarkSent", r.ID)
			}
		}
	}

	// History returns newest-first.
	hist, err := st.History(ctx, ch.ID, 0, 10)
	if err != nil || len(hist) != 2 || hist[0].Seq != 2 {
		t.Fatalf("history: n=%d err=%v", len(hist), err)
	}
}

// TestInsertMessageWithoutChatRow proves the co-sharding invariant: the message
// write path allocates seq from chat_seq and does NOT depend on the central chats
// row, so a chat_id-sharded message shard (holding only messages + chat_seq +
// outbox for its chats) can allocate a gap-free per-chat seq locally.
func TestInsertMessageWithoutChatRow(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the Postgres integration test")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ids, _ := id.NewGenerator(9)
	now := time.Now().UnixMilli()
	// A chat id NEVER inserted into `chats` — as it would be on a message shard.
	chatID := ids.NextString()
	sender := ids.NextString()

	var seqs []uint64
	for i := 0; i < 3; i++ {
		m := &model.Message{ID: ids.NextString(), ChatID: chatID, SenderID: sender, Text: "x", CreatedAt: now}
		stored, dup, err := st.InsertMessage(ctx, m, "", nil)
		if err != nil {
			t.Fatalf("insert %d without chats row: %v", i, err)
		}
		if dup {
			t.Fatalf("unexpected duplicate")
		}
		seqs = append(seqs, stored.Seq)
	}
	// Gap-free 1,2,3 despite the chat having no metadata row.
	for i, s := range seqs {
		if s != uint64(i+1) {
			t.Fatalf("seq[%d]=%d, want %d (gap-free without chats row)", i, s, i+1)
		}
	}
	if hist, err := st.History(ctx, chatID, 0, 10); err != nil || len(hist) != 3 {
		t.Fatalf("history from shard: n=%d err=%v", len(hist), err)
	}
}

// TestOutboxConcurrentClaim proves the FOR UPDATE SKIP LOCKED claim: two relays
// polling at once never receive the same record (no double-publish).
func TestOutboxConcurrentClaim(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the outbox claim test")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Clear leftover unsent rows from prior runs so the count is deterministic.
	_, _ = st.pool.Exec(ctx, `UPDATE outbox SET sent_at = 1 WHERE sent_at = 0`)

	ids, _ := id.NewGenerator(9)
	now := time.Now().UnixMilli()
	u := &model.User{ID: ids.NextString(), Username: "cc_" + ids.NextString(), PasswordHash: "x", CreatedAt: now}
	_ = st.CreateUser(ctx, u)
	ch := &model.Chat{ID: ids.NextString(), Type: model.ChatGroup, OwnerID: u.ID, CreatedAt: now}
	_ = st.CreateChat(ctx, ch)
	_ = st.AddMember(ctx, &model.ChatMember{ChatID: ch.ID, UserID: u.ID, Role: model.RoleOwner, JoinedAt: now})

	const n = 50
	for i := 0; i < n; i++ {
		m := &model.Message{ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "x", CreatedAt: now}
		_, _, err := st.InsertMessage(ctx, m, "", func(sm *model.Message) *store.OutboxRecord {
			return &store.OutboxRecord{ID: ids.NextString(), Subject: "s", Key: ch.ID, Data: []byte("{}")}
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Two concurrent claimers; collect all returned ids and assert no overlap.
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				recs, err := st.Poll(ctx, 7)
				if err != nil || len(recs) == 0 {
					return
				}
				mu.Lock()
				for _, r := range recs {
					seen[r.ID]++
				}
				mu.Unlock()
				_ = st.MarkSent(ctx, idsOf(recs))
			}
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Fatalf("claimed %d distinct records, want %d", len(seen), n)
	}
	for rid, c := range seen {
		if c != 1 {
			t.Fatalf("record %s claimed %d times (double-claim!)", rid, c)
		}
	}
}

// TestSelfChatCreate guards the fix for self-chats ("Saved Messages"): a direct
// chat where userA == userB must create cleanly (one member), not fail on a
// duplicate chat_members insert.
func TestSelfChatCreate(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the self-chat test")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ids, _ := id.NewGenerator(11)
	now := time.Now().UnixMilli()
	u := &model.User{ID: ids.NextString(), Username: "self_" + ids.NextString(), PasswordHash: "x", CreatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	ch, err := st.GetOrCreateDirect(ctx, u.ID, u.ID, ids.NextString())
	if err != nil {
		t.Fatalf("self-chat create failed: %v", err)
	}
	members, err := st.ListMembers(ctx, ch.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("self-chat members = %d (want 1), err=%v", len(members), err)
	}
	// Idempotent: a second call returns the same chat.
	ch2, err := st.GetOrCreateDirect(ctx, u.ID, u.ID, ids.NextString())
	if err != nil || ch2.ID != ch.ID {
		t.Fatalf("self-chat not idempotent: %v", err)
	}
}

func idsOf(recs []store.OutboxRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

// TestPostgresDeviceOwnership exercises the ownership scoping on the device
// upsert against real SQL: the ON CONFLICT ... WHERE and the push-token CASE are
// the parts that cannot be checked against the in-memory store. Runs only with
// SYNAPSE_TEST_PG_DSN set.
func TestPostgresDeviceOwnership(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the Postgres integration test")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ids, _ := id.NewGenerator(9)
	now := time.Now().UnixMilli()
	owner := &model.User{ID: ids.NextString(), Username: "own_" + ids.NextString(), PasswordHash: "x", CreatedAt: now}
	other := &model.User{ID: ids.NextString(), Username: "oth_" + ids.NextString(), PasswordHash: "x", CreatedAt: now}
	for _, u := range []*model.User{owner, other} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}

	devID := ids.NextString()
	if err := st.UpsertDevice(ctx, &model.Device{
		ID: devID, UserID: owner.ID, Platform: "ios", PushToken: "tok-1", CreatedAt: now, LastSeen: now,
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Another account naming the same device id must be refused, not served.
	err = st.UpsertDevice(ctx, &model.Device{
		ID: devID, UserID: other.ID, Platform: "web", CreatedAt: now, LastSeen: now,
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("squatting another user's device id: want ErrConflict, got %v", err)
	}

	// The owner's own re-login carries no push token; it must not clear the one
	// already registered.
	if err := st.UpsertDevice(ctx, &model.Device{
		ID: devID, UserID: owner.ID, Platform: "ios", CreatedAt: now, LastSeen: now + 1,
	}); err != nil {
		t.Fatalf("owner re-upsert: %v", err)
	}
	got, err := st.GetDevice(ctx, devID)
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if got.UserID != owner.ID {
		t.Fatalf("device changed hands: %s", got.UserID)
	}
	if got.PushToken != "tok-1" {
		t.Fatalf("push token lost on re-login: %q", got.PushToken)
	}
}

// TestPostgresBatchSurvivesDuplicate drives the group-commit path with one
// poisoned job: a dedup key that is already stored aborts the shared
// transaction, and the batcher must isolate it by halving rather than by
// retrying every job on its own. All jobs must still resolve — the duplicate as
// a duplicate (same message id, no new seq), its neighbours as fresh writes with
// distinct sequence numbers. Runs only with SYNAPSE_TEST_PG_DSN set.
func TestPostgresBatchSurvivesDuplicate(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the Postgres integration test")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ids, _ := id.NewGenerator(11)
	now := time.Now().UnixMilli()
	u := &model.User{ID: ids.NextString(), Username: "bat_" + ids.NextString(), PasswordHash: "x", CreatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	ch := &model.Chat{ID: ids.NextString(), Type: model.ChatGroup, OwnerID: u.ID, CreatedAt: now}
	if err := st.CreateChat(ctx, ch); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := st.AddMember(ctx, &model.ChatMember{ChatID: ch.ID, UserID: u.ID, Role: model.RoleOwner, JoinedAt: now}); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// The send the client will "retry" after a reconnect.
	first, _, err := st.InsertMessage(ctx,
		&model.Message{ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "original", CreatedAt: now},
		"retried", nil)
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	const n = 16
	type outcome struct {
		m   *model.Message
		dup bool
		err error
	}
	results := make([]outcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dedup := fmt.Sprintf("fresh-%d", i)
			if i == n/2 {
				dedup = "retried" // the poisoned job, mid-batch
			}
			m := &model.Message{ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID,
				Text: dedup, CreatedAt: time.Now().UnixMilli()}
			got, dup, err := st.InsertMessage(ctx, m, dedup, nil)
			results[i] = outcome{got, dup, err}
		}(i)
	}
	wg.Wait()

	seqs := map[uint64]bool{}
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("job %d failed instead of being isolated: %v", i, r.err)
		}
		if i == n/2 {
			if !r.dup {
				t.Fatal("the retried send was not reported as a duplicate")
			}
			if r.m.ID != first.ID {
				t.Fatalf("duplicate resolved to a different message: %s vs %s", r.m.ID, first.ID)
			}
			continue
		}
		if r.dup {
			t.Fatalf("job %d wrongly reported as duplicate", i)
		}
		if seqs[r.m.Seq] {
			t.Fatalf("seq %d handed out twice", r.m.Seq)
		}
		seqs[r.m.Seq] = true
	}
	if len(seqs) != n-1 {
		t.Fatalf("want %d distinct seqs, got %d", n-1, len(seqs))
	}
}

// TestPostgresRetentionJanitors exercises the two DELETE statements against real
// SQL. Both use a subselect with LIMIT because an unbounded DELETE on the outbox
// — the hottest table in the system — would hold locks long enough to be felt on
// the write path it exists to serve. Runs only with SYNAPSE_TEST_PG_DSN set.
func TestPostgresRetentionJanitors(t *testing.T) {
	dsn := os.Getenv("SYNAPSE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SYNAPSE_TEST_PG_DSN to run the Postgres integration test")
	}
	ctx := context.Background()
	st, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ids, _ := id.NewGenerator(13)
	now := time.Now().UnixMilli()
	u := &model.User{ID: ids.NextString(), Username: "ret_" + ids.NextString(), PasswordHash: "x", CreatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	ch := &model.Chat{ID: ids.NextString(), Type: model.ChatGroup, OwnerID: u.ID, CreatedAt: now}
	if err := st.CreateChat(ctx, ch); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := st.AddMember(ctx, &model.ChatMember{ChatID: ch.ID, UserID: u.ID, Role: model.RoleOwner, JoinedAt: now}); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Start from an empty outbox. The assertions below are about how many rows the
	// janitor removes, which is only a statement about the CODE if the table holds
	// nothing but this test's rows — the database is shared with every other
	// integration test and with whatever a previous run left behind, and an
	// assertion that depends on that reports the environment instead.
	for {
		recs, err := st.Poll(ctx, 500)
		if err != nil {
			t.Fatalf("drain poll: %v", err)
		}
		if len(recs) == 0 {
			break
		}
		drained := make([]string, len(recs))
		for i, r := range recs {
			drained[i] = r.ID
		}
		if err := st.MarkSent(ctx, drained); err != nil {
			t.Fatalf("drain mark: %v", err)
		}
	}
	for {
		n, err := st.PurgeSent(ctx, time.Now().Add(time.Minute).UnixMilli(), 500)
		if err != nil {
			t.Fatalf("drain purge: %v", err)
		}
		if n == 0 {
			break
		}
	}

	// Stage an event the way a message write does, then publish it.
	mkOb := func(m *model.Message) *store.OutboxRecord {
		return &store.OutboxRecord{ID: ids.NextString(), Subject: "message.created", Key: m.ChatID, Data: []byte(`{}`)}
	}
	if _, _, err := st.InsertMessage(ctx,
		&model.Message{ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "retained", CreatedAt: now},
		"ret-1", mkOb); err != nil {
		t.Fatalf("insert: %v", err)
	}
	recs, err := st.Poll(ctx, 10)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("nothing staged to purge")
	}
	ids2 := make([]string, len(recs))
	for i, r := range recs {
		ids2[i] = r.ID
	}
	if err := st.MarkSent(ctx, ids2); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	// An unpublished row is never eligible, and a just-published one is not yet
	// past its retention window.
	if n, err := st.PurgeSent(ctx, now-time.Hour.Milliseconds(), 100); err != nil || n != 0 {
		t.Fatalf("purge inside the retention window removed %d rows (%v)", n, err)
	}
	n, err := st.PurgeSent(ctx, time.Now().Add(time.Minute).UnixMilli(), 100)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n < len(ids2) {
		t.Fatalf("purged %d rows, expected at least %d", n, len(ids2))
	}
	// The message itself must be untouched — only the handoff copy goes.
	if _, err := st.GetMessage(ctx, ch.ID, recs[0].Key); err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unexpected message store error: %v", err)
	}

	// Scheduled sends: fired rows go, pending ones stay.
	pending := &model.ScheduledMessage{
		ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "later",
		SendAt: now + time.Hour.Milliseconds(), CreatedAt: now,
	}
	fired := &model.ScheduledMessage{
		ID: ids.NextString(), ChatID: ch.ID, SenderID: u.ID, Text: "already went",
		SendAt: now - time.Hour.Milliseconds(), CreatedAt: now,
	}
	for _, m := range []*model.ScheduledMessage{pending, fired} {
		if err := st.CreateScheduled(ctx, m); err != nil {
			t.Fatalf("create scheduled: %v", err)
		}
	}
	if _, err := st.ClaimDueScheduled(ctx, now, 10); err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if _, err := st.PurgeSentScheduled(ctx, now, 100); err != nil {
		t.Fatalf("purge scheduled: %v", err)
	}
	left, err := st.ListScheduled(ctx, u.ID, ch.ID)
	if err != nil {
		t.Fatalf("list scheduled: %v", err)
	}
	for _, m := range left {
		if m.ID == fired.ID {
			t.Fatal("a fired scheduled row survived its retention window")
		}
	}
	var kept bool
	for _, m := range left {
		kept = kept || m.ID == pending.ID
	}
	if !kept {
		t.Fatal("the janitor removed a send that has not fired yet")
	}
}
