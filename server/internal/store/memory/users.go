package memory

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// UserStore.

func (s *Store) CreateUser(_ context.Context, u *model.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.usersByName[u.Username]; ok {
		return store.ErrConflict
	}
	cp := *u
	s.users[u.ID] = &cp
	s.usersByName[u.Username] = u.ID
	return nil
}

func (s *Store) GetUser(_ context.Context, id string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (s *Store) GetUserByUsername(_ context.Context, username string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.usersByName[username]
	if !ok {
		return nil, store.ErrNotFound
	}
	u := s.users[id]
	cp := *u
	return &cp, nil
}

// UpdatePrivacy writes a user's visibility settings.
func (s *Store) UpdatePrivacy(_ context.Context, userID string, p model.Privacy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	u.Privacy = p.Normalize()
	return nil
}

func (s *Store) UpdateProfile(_ context.Context, userID, displayName, avatarRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	u.DisplayName = displayName
	u.AvatarRef = avatarRef
	return nil
}

// DeleteAccount mirrors the Postgres semantics: everything owned by the
// account is removed, messages the account sent are anonymised in place
// rather than dropped, and any forward provenance pointing at it is scrubbed
// so a forwarded copy can't re-identify a deleted sender.
func (s *Store) DeleteAccount(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	delete(s.usersByName, u.Username)
	delete(s.users, userID)
	delete(s.platformRoles, userID)

	for id, d := range s.devices {
		if d.UserID == userID {
			delete(s.devices, id)
		}
	}
	for id, sess := range s.sessions {
		if sess.UserID == userID {
			delete(s.tokenIndex, sess.Token)
			delete(s.resumeIndex, sess.ResumeToken)
			delete(s.sessions, id)
		}
	}
	// Both directions: entries this account made, and entries other people
	// made ABOUT it (including blocks, which must not outlive their subject).
	delete(s.contacts, userID)
	for owner, byUser := range s.contacts {
		delete(byUser, userID)
		if len(byUser) == 0 {
			delete(s.contacts, owner)
		}
	}
	delete(s.drafts, userID)
	for schedID, sched := range s.scheduled {
		if sched.SenderID == userID {
			delete(s.scheduled, schedID)
		}
	}
	for code, inv := range s.invites {
		if inv.CreatedBy == userID {
			delete(s.invites, code)
		}
	}
	for _, byUser := range s.reads {
		delete(byUser, userID)
	}
	for _, byUser := range s.reactions {
		delete(byUser, userID)
	}
	for _, byUser := range s.pollVotes {
		delete(byUser, userID)
	}
	for chatID, byMsg := range s.pins {
		for msgID, p := range byMsg {
			if p.PinnedBy == userID {
				delete(byMsg, msgID)
			}
		}
		if len(byMsg) == 0 {
			delete(s.pins, chatID)
		}
	}
	for _, byUser := range s.callParts {
		delete(byUser, userID)
	}
	for chatID, byUser := range s.members {
		delete(byUser, userID)
		if len(byUser) == 0 {
			delete(s.members, chatID)
		}
	}

	now := nowMs()
	for _, msgs := range s.messages {
		for _, m := range msgs {
			if m.SenderID == userID {
				m.SenderID = ""
				m.Text = ""
				m.MediaRef = ""
				m.Attachment = nil
				m.Deleted = true
				m.EditedAt = now
			}
			if m.Forward != nil && m.Forward.SenderID == userID {
				m.Forward.SenderID = ""
			}
		}
	}

	return nil
}

// UpsertDevice mirrors the Postgres semantics: a device id already owned by
// another user is refused (ErrConflict) rather than taken over, and an empty
// push token leaves the stored one alone. See the Postgres implementation for
// why — the id is client-asserted.
func (s *Store) UpsertDevice(_ context.Context, d *model.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *d
	if cur, ok := s.devices[d.ID]; ok {
		if cur.UserID != d.UserID {
			return store.ErrConflict
		}
		if cp.PushToken == "" {
			cp.PushToken = cur.PushToken
		}
		cp.CreatedAt = cur.CreatedAt
	}
	s.devices[d.ID] = &cp
	return nil
}

// SetPushToken writes a device's push token, scoped to its owner.
func (s *Store) SetPushToken(_ context.Context, userID, deviceID, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[deviceID]
	if !ok || d.UserID != userID {
		return store.ErrNotFound
	}
	d.PushToken = token
	return nil
}

func (s *Store) GetDevice(_ context.Context, id string) (*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.devices[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (s *Store) ListDevices(_ context.Context, userID string) ([]*model.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Device
	for _, d := range s.devices {
		if d.UserID == userID {
			cp := *d
			out = append(out, &cp)
		}
	}
	return out, nil
}
