// Package contact is the Contacts service: a per-user address book with
// incremental sync and a block list.
//
// Contacts are PRIVATE to their owner: the local name Alice gave Bob is Alice's
// data, not a property of Bob, and blocking is one-directional. So this is a
// per-owner table, not a shared social graph.
//
// Sync is incremental by design: a client sends the highest updated_at it has
// seen and receives only what changed since. That keeps re-syncing an address
// book cheap on a phone that reconnects constantly.
package contact

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/internal/tracing"
)

// New builds the contacts service.
func New(st store.ContactStore, users Users) *Service {
	return &Service{store: st, users: users}
}

// Add saves (or renames) a contact. The target is resolved by id or @username,
// and must exist — an address book of phantom users is worse than useless.
func (s *Service) Add(ctx context.Context, ownerID, target, name string, now int64) (*model.Contact, error) {
	ctx, span := tracing.Start(ctx, "contact.Add")
	defer span.End()

	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > MaxNameLen {
		return nil, ErrBadName
	}
	u, err := s.resolve(ctx, target)
	if err != nil {
		return nil, err
	}
	if u.ID == ownerID {
		return nil, ErrSelf
	}
	c := &model.Contact{
		OwnerID: ownerID, UserID: u.ID, Name: name,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertContact(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Remove deletes a contact. Blocking is intentionally NOT cleared here: removing
// someone from your address book must not silently un-block them.
func (s *Service) Remove(ctx context.Context, ownerID, target string) error {
	u, err := s.resolve(ctx, target)
	if err != nil {
		return err
	}
	blocked, err := s.store.IsBlocked(ctx, ownerID, u.ID)
	if err != nil {
		return err
	}
	if blocked {
		// Keep the row so the block survives; just drop the local name.
		return s.store.UpsertContact(ctx, &model.Contact{
			OwnerID: ownerID, UserID: u.ID, Name: "", Blocked: true,
		})
	}
	return s.store.DeleteContact(ctx, ownerID, u.ID)
}

// Sync returns ONE PAGE of the owner's address book changed after `since`
// (0 = full sync), plus the new cursor.
//
// The cursor is a timestamp, so a page must never end in the MIDDLE of a group
// of rows sharing a millisecond — the next request asks for "> that
// millisecond" and would skip the rest of the group. One extra row is fetched to
// see whether a next page exists, and if it does, the page is cut back to the
// last complete timestamp. Nothing is skipped and, unlike an inclusive scan,
// nothing is re-sent: an idle client that polls gets an empty page, not its last
// row again.
func (s *Service) Sync(ctx context.Context, ownerID string, since int64) ([]*model.Contact, int64, error) {
	ctx, span := tracing.Start(ctx, "contact.Sync")
	defer span.End()

	rows, err := s.store.ListContacts(ctx, ownerID, since, SyncPageSize+1)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) <= SyncPageSize {
		high := since
		for _, c := range rows {
			if c.UpdatedAt > high {
				high = c.UpdatedAt
			}
		}
		return rows, high, nil
	}
	// A next page exists: drop the trailing rows that share their timestamp with
	// the first row of it.
	boundary := rows[SyncPageSize].UpdatedAt
	page := rows[:SyncPageSize]
	for len(page) > 0 && page[len(page)-1].UpdatedAt == boundary {
		page = page[:len(page)-1]
	}
	if len(page) == 0 {
		// More than a full page shares a single millisecond — a timestamp cursor
		// cannot express a position inside it. Deliver the page and step past that
		// millisecond: a stalled sync is worse than a skip that needs SyncPageSize
		// writes inside one millisecond, for one user, to provoke.
		return rows[:SyncPageSize], boundary, nil
	}
	return page, page[len(page)-1].UpdatedAt, nil
}

// SetBlocked blocks or unblocks a user. Blocking works on strangers too (that is
// the point), so no prior contact is required.
func (s *Service) SetBlocked(ctx context.Context, ownerID, target string, blocked bool, now int64) error {
	u, err := s.resolve(ctx, target)
	if err != nil {
		return err
	}
	if u.ID == ownerID {
		return ErrSelf
	}
	return s.store.SetBlocked(ctx, ownerID, u.ID, blocked, now)
}

// IsBlocked reports whether owner has blocked user.
func (s *Service) IsBlocked(ctx context.Context, ownerID, userID string) (bool, error) {
	return s.store.IsBlocked(ctx, ownerID, userID)
}

// BlocksBetween reports whether EITHER side has blocked the other. Delivery uses
// this: a block must stop traffic in both directions, or blocking someone would
// still let them read your replies.
func (s *Service) BlocksBetween(ctx context.Context, a, b string) (bool, error) {
	if ab, err := s.store.IsBlocked(ctx, a, b); err != nil || ab {
		return ab, err
	}
	return s.store.IsBlocked(ctx, b, a)
}

// resolve accepts a user id or an "@username".
func (s *Service) resolve(ctx context.Context, target string) (*model.User, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, store.ErrNotFound
	}
	if strings.HasPrefix(target, "@") {
		return s.users.GetUserByUsername(ctx, strings.TrimPrefix(target, "@"))
	}
	return s.users.GetUser(ctx, target)
}
