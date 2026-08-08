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
	"time"

	"github.com/synapse-chat/synapse/internal/metrics"
)

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

func (m *memoryBuffer) Drop(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
	return nil
}
