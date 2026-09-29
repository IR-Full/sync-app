package memory

import (
	"context"
	"sort"
	"strconv"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

// ChatSummaryReader / MemberFlagStore.

// UserChatSummaries mirrors the Postgres one-query page: activity order, last
// message, unread count, the caller's flags.
//
// It sorts the whole membership and then cuts, which is what the SQL version asks
// the database to do with an index. That is acceptable here and not there: this
// backend holds a development-sized dataset in one process, while the production
// one has to page a real list without reading it whole. The behaviour must match
// exactly either way, which is what the conformance suite is for.
func (s *Store) UserChatSummaries(_ context.Context, userID string, afterActivity int64, afterChatID string, limit int, includeArchived bool) ([]model.ChatSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows []model.ChatSummary
	for chatID, members := range s.members {
		me, ok := members[userID]
		if !ok {
			continue
		}
		if me.Flags.Archived && !includeArchived {
			continue
		}
		c, ok := s.chats[chatID]
		if !ok {
			continue // a chat that vanished under the membership is skipped, not fatal
		}
		cp := *c
		sum := model.ChatSummary{Chat: &cp, MyRole: me.Role, Flags: me.Flags}

		// Newest LIVE message. Deleted rows are tombstones: they keep their seq so
		// ordering stays gap-free, but a chat list must not preview one.
		msgs := s.messages[chatID]
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Deleted {
				continue
			}
			lm := *msgs[i]
			sum.LastMessage = &lm
			break
		}
		sum.LastActivityAt = cp.CreatedAt
		if sum.LastMessage != nil {
			sum.LastActivityAt = sum.LastMessage.CreatedAt
			read := uint64(0)
			if rs := s.reads[chatID]; rs != nil {
				if st := rs[userID]; st != nil {
					read = st.UpToSeq
				}
			}
			if read > sum.LastMessage.Seq {
				// A cursor ahead of the newest live message is not a negative unread
				// count; it is a cursor set against a message that has since been
				// deleted.
				read = sum.LastMessage.Seq
			}
			sum.UnreadCount = int64(sum.LastMessage.Seq - read)
		}
		// Both direct and SECRET chats are two-party and have no title, so the row
		// cannot be named without the peer. Is1To1 rather than a comparison against
		// ChatDirect: the secret type was added later, and a type check written as
		// equality is exactly the kind that silently omits it.
		if cp.Type.Is1To1() {
			for uid := range members {
				if uid != userID {
					sum.PeerID = uid
					break
				}
			}
		}
		rows = append(rows, sum)
	}

	// Activity descending, chat id descending as the tiebreak — the same total
	// order the keyset cursor walks, so a page boundary cannot repeat or skip.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LastActivityAt != rows[j].LastActivityAt {
			return rows[i].LastActivityAt > rows[j].LastActivityAt
		}
		return !lessChatID(rows[i].Chat.ID, rows[j].Chat.ID)
	})

	out := make([]model.ChatSummary, 0, limit)
	for _, r := range rows {
		if afterActivity != 0 || afterChatID != "" {
			// Strictly after the cursor in the same total order as the sort above.
			if r.LastActivityAt > afterActivity {
				continue
			}
			if r.LastActivityAt == afterActivity && !lessChatID(r.Chat.ID, afterChatID) {
				continue
			}
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// lessChatID compares ids numerically where both are numeric, so "9" sorts below
// "10" the way the Postgres BIGINT column does. Falls back to lexicographic for
// non-numeric ids, which only tests use.
func lessChatID(a, b string) bool {
	na, ea := strconv.ParseInt(a, 10, 64)
	nb, eb := strconv.ParseInt(b, 10, 64)
	if ea == nil && eb == nil {
		return na < nb
	}
	return a < b
}

func (s *Store) SetMemberFlags(_ context.Context, chatID, userID string, f model.MemberFlags) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	members := s.members[chatID]
	if members == nil {
		return store.ErrNotFound
	}
	m, ok := members[userID]
	if !ok {
		return store.ErrNotFound
	}
	cp := *m
	cp.Flags = f
	cp.Muted = f.MutedUntil != 0 // keep the legacy column consistent
	members[userID] = &cp
	return nil
}

// CountPinnedChatsExcept counts this user's pinned chats, ignoring one.
func (s *Store) CountPinnedChatsExcept(_ context.Context, userID, exceptChatID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for chatID, members := range s.members {
		if chatID == exceptChatID {
			continue
		}
		if m, ok := members[userID]; ok && m.Flags.Pinned {
			n++
		}
	}
	return n, nil
}

func (s *Store) GetMemberFlags(_ context.Context, chatID, userID string) (model.MemberFlags, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if members := s.members[chatID]; members != nil {
		if m, ok := members[userID]; ok {
			return m.Flags, nil
		}
	}
	return model.MemberFlags{}, store.ErrNotFound
}
