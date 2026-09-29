package memory

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/model"
)

// SecretQueueStore.

// secretKey addresses the queue. A secret message is encrypted to ONE device's
// ratchet session, so the queue is per (user, device) and not per user.
func secretKey(userID, deviceID string) string { return userID + "|" + deviceID }

func (s *Store) EnqueueSecret(_ context.Context, e *model.SecretEnvelope, maxPerDevice int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := secretKey(e.ToUserID, e.ToDeviceID)
	cp := *e
	cp.Header = append([]byte(nil), e.Header...)
	cp.Ciphertext = append([]byte(nil), e.Ciphertext...)
	q := append(s.secretQ[k], &cp)
	// Drop from the FRONT once over the cap: the oldest undelivered ciphertext is
	// the one whose ratchet session is least likely to still exist on either side.
	if maxPerDevice > 0 && len(q) > maxPerDevice {
		q = append([]*model.SecretEnvelope(nil), q[len(q)-maxPerDevice:]...)
	}
	s.secretQ[k] = q
	return nil
}

func (s *Store) PendingSecrets(_ context.Context, toUserID, toDeviceID, afterID string, limit int) ([]*model.SecretEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]*model.SecretEnvelope, 0, limit)
	seen := afterID == ""
	for _, e := range s.secretQ[secretKey(toUserID, toDeviceID)] {
		if !seen {
			// The cursor is the id of the last envelope the client took, so the
			// entry that MATCHES it is the boundary, not a result.
			if e.ID == afterID {
				seen = true
			}
			continue
		}
		cp := *e
		out = append(out, &cp)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *Store) AckSecrets(_ context.Context, toUserID, toDeviceID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Scoped by owner: only this device's queue is touched, so an id belonging to
	// someone else's queue matches nothing no matter who names it.
	k := secretKey(toUserID, toDeviceID)
	q := s.secretQ[k]
	kept := q[:0]
	n := 0
	for _, e := range q {
		if drop[e.ID] {
			n++
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		delete(s.secretQ, k)
	} else {
		s.secretQ[k] = kept
	}
	return n, nil
}

func (s *Store) PurgeExpiredSecrets(_ context.Context, now int64, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 1000
	}
	n := 0
	for k, q := range s.secretQ {
		kept := q[:0]
		for _, e := range q {
			if e.ExpiresAt != 0 && e.ExpiresAt <= now && n < limit {
				n++
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			delete(s.secretQ, k)
		} else {
			s.secretQ[k] = kept
		}
	}
	return n, nil
}
