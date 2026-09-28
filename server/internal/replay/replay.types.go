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
	// HighWater is the largest seq the buffer still holds for a session, or 0 if
	// it holds nothing.
	//
	// It exists because "nothing to replay" and "no such session" are different
	// facts and the resume path has to tell them apart. A client that has
	// acknowledged everything gets no frames back — and the gateway used to read
	// that as "no evidence of any seq" and reset the connection's outbound counter
	// to zero, so the next frame it sent was numbered 1 after the client had
	// already acknowledged 100. This is the server's OWN record of how far it got,
	// which is what the clamp needed all along: authoritative without taking the
	// client's word for it.
	HighWater(ctx context.Context, sessionID string) (uint64, error)
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
