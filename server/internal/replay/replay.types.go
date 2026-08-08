package replay

import (
	"context"
	"sync"
	"time"
)

// Frame is one buffered outbound frame (encoded envelope payload) with its seq.
type Frame struct {
	Seq     uint64
	Payload []byte
}

// Buffer stores recent outbound frames per session.
type Buffer interface {
	// Append records a sent frame (bounded ring per session).
	Append(ctx context.Context, sessionID string, seq uint64, payload []byte) error
	// Since returns buffered frames with Seq > afterSeq, in ascending order.
	Since(ctx context.Context, sessionID string, afterSeq uint64) ([]Frame, error)
	// Drop clears a session's buffer (e.g. on logout).
	Drop(ctx context.Context, sessionID string) error
}

// memoryBuffer is a per-process ring (single node / dev / tests).
type memoryBuffer struct {
	mu        sync.Mutex
	sessions  map[string]*sessionBuf
	lastSweep time.Time
}

type sessionBuf struct {
	frames   []Frame
	lastSeen time.Time
}
