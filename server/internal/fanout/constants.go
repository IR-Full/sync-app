package fanout

import "time"

const memberCacheTTL = 3 * time.Second

// The member cache is bounded the same way the chat service bounds its
// authorization view: expired entries are not self-removing, so without a sweep
// the map keeps one entry per chat this worker has ever delivered to — and each
// entry holds that chat's entire member list. memberCacheMax also caps the sweep
// cost, keeping it short enough to run under the write lock.
const (
	memberCacheMax   = 50_000
	memberSweepEvery = 30 * time.Second
)

// kindCacheMax bounds the chat-kind cache. An entry is a few dozen bytes, so this
// is a memory cap of a few MB; past it the cache is dropped wholesale and refills
// from the chats that are actually active.
const kindCacheMax = 100_000

// Hot-chat fanout sharding. A message to a huge channel would otherwise make ONE
// worker walk every member serially (fanout amplification). Above the threshold,
// the member set is split into fixed-size chunks re-published as fanout.shard
// jobs; because they share the "fanout" queue group, competing workers deliver
// the chunks in parallel, so a million-member fanout scales with worker count.
const (
	fanoutShardThreshold = 1000           // deliver inline at or below this many members
	fanoutShardSize      = 500            // members per shard job above the threshold
	subjFanoutShard      = "fanout.shard" // internal fan-out sub-job subject
)

// Presence audience paging. A user's direct chats are walked in pages so the
// lookup cannot materialize an unbounded list for someone with a very large
// address book; the page cap bounds the work one presence transition may cause,
// and anyone beyond it simply learns the state from the next transition.
const (
	presencePageSize = 200
	maxPresencePages = 10
)

// routeBatchSize bounds ONE router batch lookup and the node→recipients map built
// from it.
//
// The bound has to live here rather than being inherited from whatever page size
// the caller happens to use: a Redis pipeline of unbounded length is a single
// request that can stall the connection, and a map keyed by node holding every
// recipient of a million-member channel is the coordinator heap problem that
// hot-chat sharding already exists to avoid. 200 is comfortably inside one
// network MTU's worth of small HKEYS commands and turns any realistic group into
// a single round trip.
const routeBatchSize = 200
