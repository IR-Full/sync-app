package postgres

import (
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
)

// The batcher implements GROUP COMMIT for message writes. Load testing showed the
// single-node write ceiling is Postgres commit fsync: one transaction per message
// means one fsync per message. The batcher coalesces concurrent InsertMessage
// calls arriving within a tiny window into ONE transaction with ONE commit, so N
// messages amortize a single fsync — the same win as synchronous_commit=off but
// WITHOUT giving up durability. Callers block until their batch commits, then get
// their assigned seq/id back.
//
// Correctness: seq allocation stays exact (each message's UPDATE ... RETURNING
// runs sequentially inside the shared tx, so same-chat messages get consecutive
// seqs). If a batch tx fails (e.g. a rare concurrent dedup race), the batcher
// falls back to per-message transactions so every job still resolves correctly.
type batcher struct {
	store    *Store
	jobs     chan *writeJob
	done     chan struct{}
	maxBatch int
	maxWait  time.Duration
}

type writeJob struct {
	m        *model.Message
	dedupKey string
	mkOb     store.MakeOutbox
	result   chan writeResult
}

type writeResult struct {
	stored *model.Message
	dup    bool
	err    error
}
