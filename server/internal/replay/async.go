package replay

import (
	"context"
	"log/slog"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/metrics"
)

/*
Async takes the replay store off the connection's write path.

The gateway appends every outbound frame to the resume buffer from writeLoop —
the SINGLE writer for that connection. Done synchronously, each frame waited on a
Redis round trip (bounded at 250 ms), so a slow Redis throttled every connection
on the node to a few frames a second and a dead one filled the outbound lanes
until the connection was dropped: an optimisation's dependency taking users
offline.

Here Append only enqueues. One worker per process drains the queue in batches
(one pipelined round trip per batch when the store supports it). The queue is
bounded; when it is full the entry is dropped and counted, which costs exactly
what a failed synchronous append always cost — the resuming client falls back to
history backfill (see gateway doResume).

Since, HighWater and Drop flush the queue first, so a resume served by this node
sees every frame this node sent. A resume on ANOTHER node can miss the last few
milliseconds of frames still queued here; the reconnect itself takes longer than
a flush interval, and the fallback for a miss is the same backfill.
*/

// Entry is one queued frame.
type Entry struct {
	SessionID string
	Seq       uint64
	Payload   []byte
}

// BatchAppender is implemented by stores that can write many entries in one
// round trip. Optional: Async falls back to one Append per entry.
type BatchAppender interface {
	AppendBatch(ctx context.Context, entries []Entry) error
}

// Async is a Buffer whose Append never blocks.
type Async struct {
	inner    Buffer
	q        chan Entry
	flushReq chan chan struct{}
	stop     chan struct{}
	stopped  chan struct{}
	log      *slog.Logger
}

const (
	asyncMaxBatch     = 256
	asyncWriteTimeout = 2 * time.Second
)

// NewAsync wraps inner and starts its writer. queue bounds the number of frames
// waiting to be written across the whole process.
func NewAsync(inner Buffer, queue int, log *slog.Logger) *Async {
	if queue <= 0 {
		queue = 1 << 16
	}
	if log == nil {
		log = slog.Default()
	}
	a := &Async{
		inner:    inner,
		q:        make(chan Entry, queue),
		flushReq: make(chan chan struct{}),
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
		log:      log,
	}
	go a.run()
	return a
}

// Append enqueues a frame and returns immediately. It never fails: a full queue
// drops the frame (counted in metrics.ReplayDropped).
func (a *Async) Append(_ context.Context, sessionID string, seq uint64, payload []byte) error {
	select {
	case a.q <- Entry{SessionID: sessionID, Seq: seq, Payload: payload}:
	default:
		metrics.ReplayDropped.Inc()
	}
	return nil
}

// Since flushes queued frames, then reads from the store.
func (a *Async) Since(ctx context.Context, sessionID string, afterSeq uint64) ([]Frame, error) {
	a.Flush(ctx)
	return a.inner.Since(ctx, sessionID, afterSeq)
}

// HighWater flushes queued frames, then reads from the store.
func (a *Async) HighWater(ctx context.Context, sessionID string) (uint64, error) {
	a.Flush(ctx)
	return a.inner.HighWater(ctx, sessionID)
}

// Drop flushes queued frames (so none land after the drop), then clears.
func (a *Async) Drop(ctx context.Context, sessionID string) error {
	a.Flush(ctx)
	return a.inner.Drop(ctx, sessionID)
}

// Flush waits until everything enqueued before the call has been written, or
// until ctx is done.
func (a *Async) Flush(ctx context.Context) {
	ack := make(chan struct{})
	select {
	case a.flushReq <- ack:
	case <-a.stopped:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-ack:
	case <-ctx.Done():
	}
}

// Close drains the queue and stops the writer.
func (a *Async) Close() {
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
	<-a.stopped
}

func (a *Async) run() {
	defer close(a.stopped)
	batch := make([]Entry, 0, asyncMaxBatch)
	for {
		select {
		case e := <-a.q:
			batch = a.drain(append(batch[:0], e), asyncMaxBatch)
			a.write(batch)
		case ack := <-a.flushReq:
			// Everything enqueued before the request is already in the channel.
			for {
				batch = a.drain(batch[:0], asyncMaxBatch)
				if len(batch) == 0 {
					break
				}
				a.write(batch)
			}
			close(ack)
		case <-a.stop:
			for {
				batch = a.drain(batch[:0], asyncMaxBatch)
				if len(batch) == 0 {
					return
				}
				a.write(batch)
			}
		}
	}
}

// drain appends queued entries without blocking, up to max in total.
func (a *Async) drain(batch []Entry, max int) []Entry {
	for len(batch) < max {
		select {
		case e := <-a.q:
			batch = append(batch, e)
		default:
			return batch
		}
	}
	return batch
}

func (a *Async) write(batch []Entry) {
	ctx, cancel := context.WithTimeout(context.Background(), asyncWriteTimeout)
	defer cancel()
	var err error
	if b, ok := a.inner.(BatchAppender); ok {
		err = b.AppendBatch(ctx, batch)
	} else {
		for _, e := range batch {
			if e2 := a.inner.Append(ctx, e.SessionID, e.Seq, e.Payload); e2 != nil && err == nil {
				err = e2
			}
		}
	}
	if err != nil {
		metrics.ReplayWriteErrors.Inc()
		a.log.Debug("replay batch write failed", "frames", len(batch), "err", err)
	}
}
