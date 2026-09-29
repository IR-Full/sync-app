package memory

import (
	"context"
	"sort"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// PollStore.

func (s *Store) CreatePoll(_ context.Context, p *model.Poll) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	s.polls[p.ID] = &cp
	s.pollByMsg[p.MessageID] = p.ID
	return nil
}

func (s *Store) GetPoll(_ context.Context, id string) (*model.Poll, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.polls[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *Store) GetPollByMessage(ctx context.Context, messageID string) (*model.Poll, error) {
	s.mu.RLock()
	id, ok := s.pollByMsg[messageID]
	s.mu.RUnlock()
	if !ok {
		return nil, store.ErrNotFound
	}
	return s.GetPoll(ctx, id)
}

func (s *Store) ClosePoll(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.polls[id]
	if !ok {
		return store.ErrNotFound
	}
	p.Closed = true
	return nil
}

func (s *Store) Vote(_ context.Context, v *model.PollVote, multiChoice bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-checked under the same lock as the write, so a concurrent ClosePoll
	// cannot slip between the caller's check and this one.
	p, ok := s.polls[v.PollID]
	if !ok {
		return false, store.ErrNotFound
	}
	if p.Closed {
		return false, store.ErrPollClosed
	}
	if s.pollVotes[v.PollID] == nil {
		s.pollVotes[v.PollID] = map[string]map[int32]bool{}
	}
	cur := s.pollVotes[v.PollID][v.UserID]
	if cur == nil {
		cur = map[int32]bool{}
		s.pollVotes[v.PollID][v.UserID] = cur
	}
	if multiChoice {
		if cur[v.OptionIndex] { // toggle off
			delete(cur, v.OptionIndex)
			return false, nil
		}
	} else {
		clear(cur) // single choice replaces
	}
	cur[v.OptionIndex] = true
	return true, nil
}

func (s *Store) Tally(_ context.Context, pollID string) (map[int32]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[int32]int{}
	for _, opts := range s.pollVotes[pollID] {
		for idx := range opts {
			out[idx]++
		}
	}
	return out, nil
}

func (s *Store) VotedOptions(_ context.Context, pollID, userID string) ([]int32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []int32
	for idx := range s.pollVotes[pollID][userID] {
		out = append(out, idx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
