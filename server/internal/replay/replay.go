// Package replay is the per-session outbound replay buffer that makes session
// RESUME lossless. As the gateway sends frames to a device it appends each
// (seq, frame) to the session's buffer; on reconnect the client sends RESUME
// with the last seq it received, and the gateway replays exactly the frames it
// missed — instead of forcing a full history refetch. Backed by Redis so resume
// works even if the client reconnects to a different node. It is OPTIONAL
// (per-frame writes cost); when unset the gateway falls back to history sync.
package replay

import (
	"context"
	"sync"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/metrics"
)

// maxFrames caps how many recent frames a session buffers.
const maxFrames = 1024

// A buffer is only useful while a client might still reconnect into it, and
// nothing calls Drop on a normal disconnect — the whole point is to survive one.
// So sessions expire: sessionTTL is how long after its last frame a session's
// buffer is kept, and the sweep runs on writes (there is no timer goroutine to
// leak). The Redis backend gets the same behaviour for free from key TTLs.
const (
	sessionTTL  = 10 * time.Minute
	sweepEvery  = time.Minute
	maxSessions = 100_000
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

// NewMemory returns an in-process replay buffer.
func NewMemory() Buffer {
	return &memoryBuffer{sessions: map[string]*sessionBuf{}, lastSweep: time.Now()}
}

func (m *memoryBuffer) Append(_ context.Context, sessionID string, seq uint64, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(payload))
	copy(cp, payload)
	m.sweepLocked()
	s := m.sessions[sessionID]
	if s == nil {
		s = &sessionBuf{}
		m.sessions[sessionID] = s
	}
	s.frames = append(s.frames, Frame{Seq: seq, Payload: cp})
	if len(s.frames) > maxFrames {
		s.frames = s.frames[len(s.frames)-maxFrames:]
	}
	s.lastSeen = time.Now()
	return nil
}

// sweepLocked expires idle sessions and enforces the ceiling. Caller holds the
// lock. Dropping a buffer costs a resuming client nothing worse than a history
// backfill, which is exactly what it would get if the buffer had never existed.
func (m *memoryBuffer) sweepLocked() {
	now := time.Now()
	if now.Sub(m.lastSweep) < sweepEvery && len(m.sessions) < maxSessions {
		return
	}
	m.lastSweep = now
	for id, s := range m.sessions {
		if now.Sub(s.lastSeen) > sessionTTL {
			delete(m.sessions, id)
		}
	}
	for id := range m.sessions {
		if len(m.sessions) < maxSessions {
			break
		}
		delete(m.sessions, id)
	}
	metrics.CacheEntries.WithLabelValues("replay_sessions").Set(float64(len(m.sessions)))
}

func (m *memoryBuffer) Since(_ context.Context, sessionID string, afterSeq uint64) ([]Frame, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil {
		return nil, nil
	}
	s.lastSeen = time.Now() // a resume is proof the session is still wanted
	var out []Frame
	for _, f := range s.frames {
		if f.Seq > afterSeq {
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *memoryBuffer) HighWater(_ context.Context, sessionID string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sessionID]
	if s == nil || len(s.frames) == 0 {
		return 0, nil
	}
	// The last frame, not a scan: Append only ever adds ascending seqs and trims
	// from the front, so the tail is the maximum by construction.
	return s.frames[len(s.frames)-1].Seq, nil
}

func (m *memoryBuffer) Drop(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
	return nil
}
