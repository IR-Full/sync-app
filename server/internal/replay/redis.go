package replay

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedis returns a Redis-backed replay buffer.
func NewRedis(rdb *redis.Client, ttl time.Duration) Buffer {
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	return &redisBuffer{rdb: rdb, ttl: ttl}
}

func streamKey(sessionID string) string { return "resume:" + sessionID }

/*
The frame's seq IS its stream id.

Redis stream ids are `<a>-<b>` and must strictly increase, which is exactly what
a per-session outbound seq already is — so using `<seq>-1` as the id turns
"give me everything after seq N" into a range query the server performs, instead
of a full-stream read the client filters.

That is the whole fix. `Since` used to call `XRANGE key - +`, pulling every
buffered frame (up to maxFrames = 1024) over the wire on every resume and then
discarding the ones at or below the cursor in Go. A client that had acknowledged
all but the last frame still paid for a thousand.

The `-1` suffix rather than `-0` is deliberate: seq starts at 1 in practice, but
`0-0` is not a legal Redis stream id, so anchoring the sequence part at 1 keeps
seq 0 representable if it ever occurs.
*/
func streamID(seq uint64) string { return strconv.FormatUint(seq, 10) + "-1" }

// exclusiveFrom builds the open-interval start for a range query. Redis reads
// `(id` as "after this id, not including it", which is precisely the `Seq >
// afterSeq` the interface promises — done by the server rather than by the caller.
func exclusiveFrom(afterSeq uint64) string { return "(" + streamID(afterSeq) }

func (b *redisBuffer) Append(ctx context.Context, sessionID string, seq uint64, payload []byte) error {
	key := streamKey(sessionID)
	pipe := b.rdb.TxPipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: maxFrames,
		Approx: true,
		// The explicit id is what makes Since a range query. It also makes a seq
		// that fails to advance a visible error instead of a silently misordered
		// stream — the id has to be greater than the last one, which is the same
		// invariant the connection's outbound counter already claims to hold.
		ID:     streamID(seq),
		Values: map[string]any{"p": payload},
	})
	pipe.Expire(ctx, key, b.ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// AppendBatch writes many frames in one pipelined round trip, refreshing each
// touched session's TTL once. Not a transaction: frames are independent, and a
// failed XADD (say, a seq that did not advance) must not discard its neighbours.
func (b *redisBuffer) AppendBatch(ctx context.Context, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	pipe := b.rdb.Pipeline()
	touched := make(map[string]struct{}, 4)
	for _, e := range entries {
		key := streamKey(e.SessionID)
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: key, MaxLen: maxFrames, Approx: true,
			ID: streamID(e.Seq), Values: map[string]any{"p": e.Payload},
		})
		touched[key] = struct{}{}
	}
	for key := range touched {
		pipe.Expire(ctx, key, b.ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (b *redisBuffer) Since(ctx context.Context, sessionID string, afterSeq uint64) ([]Frame, error) {
	msgs, err := b.rdb.XRange(ctx, streamKey(sessionID), exclusiveFrom(afterSeq), "+").Result()
	if err != nil {
		return nil, err
	}
	out := make([]Frame, 0, len(msgs))
	for _, m := range msgs {
		seq, ok := seqFromID(m.ID)
		if !ok {
			continue // not a frame this version wrote; skip rather than misorder
		}
		out = append(out, Frame{Seq: seq, Payload: []byte(toStr(m.Values["p"]))})
	}
	return out, nil
}

// HighWater reads the last id in the stream — one entry, not the whole stream.
func (b *redisBuffer) HighWater(ctx context.Context, sessionID string) (uint64, error) {
	msgs, err := b.rdb.XRevRangeN(ctx, streamKey(sessionID), "+", "-", 1).Result()
	if err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	seq, _ := seqFromID(msgs[0].ID)
	return seq, nil
}

func (b *redisBuffer) Drop(ctx context.Context, sessionID string) error {
	return b.rdb.Del(ctx, streamKey(sessionID)).Err()
}

// seqFromID recovers the frame seq from a stream id.
func seqFromID(id string) (uint64, bool) {
	dash := strings.IndexByte(id, '-')
	if dash < 0 {
		return 0, false
	}
	seq, err := strconv.ParseUint(id[:dash], 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
