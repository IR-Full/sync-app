package postgres

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
)

func newBatcher(s *Store) *batcher {
	b := &batcher{
		store:    s,
		jobs:     make(chan *writeJob, 8192),
		done:     make(chan struct{}),
		maxBatch: envInt("SYNAPSE_WRITE_BATCH_SIZE", 64),
		maxWait:  time.Duration(envInt("SYNAPSE_WRITE_BATCH_WAIT_US", 2000)) * time.Microsecond,
	}
	go b.run()
	return b
}

func (b *batcher) stop() { close(b.done) }

// submit enqueues a write and waits for the batch to commit.
func (b *batcher) submit(ctx context.Context, m *model.Message, dedupKey string, mkOb store.MakeOutbox) (*model.Message, bool, error) {
	j := &writeJob{m: m, dedupKey: dedupKey, mkOb: mkOb, result: make(chan writeResult, 1)}
	select {
	case b.jobs <- j:
	case <-b.done:
		return nil, false, errors.New("postgres: store closed")
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	select {
	case r := <-j.result:
		return r.stored, r.dup, r.err
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

// run collects jobs into batches and flushes them.
func (b *batcher) run() {
	for {
		select {
		case <-b.done:
			return
		case first := <-b.jobs:
			batch := b.collect(first)
			b.flush(batch)
		}
	}
}

// collect gathers up to maxBatch jobs, waiting at most maxWait for the batch to
// fill (so latency stays bounded under light load).
func (b *batcher) collect(first *writeJob) []*writeJob {
	batch := make([]*writeJob, 0, b.maxBatch)
	batch = append(batch, first)
	timer := time.NewTimer(b.maxWait)
	defer timer.Stop()
	for len(batch) < b.maxBatch {
		select {
		case j := <-b.jobs:
			batch = append(batch, j)
		case <-timer.C:
			return batch
		case <-b.done:
			return batch
		}
	}
	return batch
}

// flush commits a batch in one transaction, BISECTING on failure so that one bad
// job costs a few extra commits instead of one per message.
//
// The batched path deliberately skips a dedup pre-SELECT (a round trip on the
// hot path) and lets the partial unique index catch duplicates — but that aborts
// the whole transaction, and the protocol tells clients to retry unacked sends
// with the same dedup key. A reconnect storm therefore delivers duplicates in
// bulk, exactly when load is highest; retrying job-by-job there would turn one
// fsync into N and collapse write throughput at the worst possible moment.
// Halving isolates the offender in log(N) commits and keeps its innocent
// neighbours batched.
func (b *batcher) flush(batch []*writeJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b.flushOrSplit(ctx, batch)
}

func (b *batcher) flushOrSplit(ctx context.Context, batch []*writeJob) {
	if len(batch) == 0 {
		return
	}
	if err := b.flushTx(ctx, batch); err == nil {
		return
	}
	// A single job that fails is the one at fault: resolve it on its own, with the
	// dedup-aware path that returns "duplicate" instead of an error.
	if len(batch) == 1 {
		j := batch[0]
		stored, dup, err := b.store.insertOne(ctx, j.m, j.dedupKey, j.mkOb)
		j.result <- writeResult{stored: stored, dup: dup, err: err}
		return
	}
	metrics.WriteBatchSplit.Inc()
	mid := len(batch) / 2
	b.flushOrSplit(ctx, batch[:mid])
	b.flushOrSplit(ctx, batch[mid:])
}

// flushTx writes every job in one transaction with a single commit (one fsync).
// Statements are PIPELINED with pgx.Batch so the whole batch costs ~2 network
// round-trips (seq allocation, then inserts) instead of ~3×N — critical because
// the intra-tx round-trips, not fsync, dominate once fsync is amortized.
// Results are delivered only after a successful commit.
func (b *batcher) flushTx(ctx context.Context, batch []*writeJob) error {
	tx, err := b.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Phase 1 (pipelined): allocate each chat's next seq from chat_seq (upsert).
	// Same-chat messages get consecutive seqs because the server executes the
	// queued statements in order. Self-contained (no chats-row dependency) so the
	// batch works on a chat_id-sharded message shard.
	seqBatch := &pgx.Batch{}
	for _, j := range batch {
		seqBatch.Queue(seqBumpSQL, atoi(j.m.ChatID))
	}
	br := tx.SendBatch(ctx, seqBatch)
	seqs := make([]int64, len(batch))
	for i := range batch {
		if err := br.QueryRow().Scan(&seqs[i]); err != nil {
			br.Close()
			return err // fall back per-job
		}
	}
	if err := br.Close(); err != nil {
		return err
	}

	// Phase 2 (pipelined): insert each message + its outbox event, then NOTIFY.
	stored := make([]*model.Message, len(batch))
	ins := &pgx.Batch{}
	for i, j := range batch {
		var replyTo int64
		if j.m.ReplyTo != "" {
			replyTo = atoi(j.m.ReplyTo)
		}
		ins.Queue(
			`INSERT INTO messages (id, chat_id, sender_id, seq, text, media_ref, reply_to, dedup_key, created_at, attachment, thread_root, fwd_chat_id, fwd_msg_id, fwd_sender_id, expires_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			atoi(j.m.ID), atoi(j.m.ChatID), atoi(j.m.SenderID), seqs[i], j.m.Text, j.m.MediaRef, replyTo, j.dedupKey, j.m.CreatedAt,
			attachJSON(j.m.Attachment), atoi(j.m.ThreadRoot),
			fwdChat(j.m), fwdMsg(j.m), fwdSender(j.m), j.m.ExpiresAt)
		if j.m.ThreadRoot != "" {
			// Keep the root's reply tally in the same batch transaction.
			ins.Queue(`UPDATE messages SET reply_count = reply_count + 1 WHERE chat_id=$1 AND id=$2`,
				atoi(j.m.ChatID), atoi(j.m.ThreadRoot))
		}
		cp := *j.m
		cp.Seq = uint64(seqs[i])
		stored[i] = &cp
		if j.mkOb != nil {
			if rec := j.mkOb(&cp); rec != nil {
				ins.Queue(
					`INSERT INTO outbox (id, subject, key, payload, created_at) VALUES ($1,$2,$3,$4,$5)`,
					atoi(rec.ID), rec.Subject, rec.Key, rec.Data, nowMs())
			}
		}
	}
	ins.Queue(`NOTIFY synapse_outbox`) // one wakeup for the whole batch
	br2 := tx.SendBatch(ctx, ins)
	for i := 0; i < ins.Len(); i++ {
		if _, err := br2.Exec(); err != nil {
			br2.Close()
			return err // dedup race etc → fall back per-job
		}
	}
	if err := br2.Close(); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	metrics.WriteBatch.Observe(float64(len(batch)))
	for i, j := range batch {
		j.result <- writeResult{stored: stored[i]}
	}
	return nil
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
