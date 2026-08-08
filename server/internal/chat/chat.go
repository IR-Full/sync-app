// Package chat is the Chat Service (Section 6). It owns conversations and
// membership, resolves the canonical 1:1 chat for a user pair, and answers the
// authorization question "may this user post/read here?". Ordering sequence
// allocation (BumpSeq) also lives here because the counter is a property of the
// chat aggregate.
package chat

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/synapse-chat/synapse/internal/metrics"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/id"
)

// New builds the chat service.
func New(chats store.ChatStore, ids *id.Generator) *Service {
	return &Service{
		chats: chats, ids: ids,
		cache: map[string]*authEntry{}, roleCache: map[string]memberRole{},
		lastSweep: time.Now(),
	}
}

// authView returns a chat's cached authorization view, loading it on
// miss/expiry. It reads one member page more than it is willing to cache: that
// extra row is how it learns the chat is too large to hold, without loading the
// chat to find out.
func (s *Service) authView(ctx context.Context, chatID string) (*authEntry, error) {
	s.mu.RLock()
	e, ok := s.cache[chatID]
	s.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return e, nil
	}
	c, err := s.chats.GetChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	members, err := s.chats.ListMembersPage(ctx, chatID, "", maxCachedMembers+1)
	if err != nil {
		return nil, err
	}
	e = &authEntry{typ: c.Type, expires: time.Now().Add(authCacheTTL)}
	if len(members) > maxCachedMembers {
		e.large = true
	} else {
		e.roles = make(map[string]model.MemberRole, len(members))
		for _, m := range members {
			e.roles[m.UserID] = m.Role
		}
	}
	s.mu.Lock()
	s.sweepLocked()
	s.cache[chatID] = e
	s.mu.Unlock()
	return e, nil
}

// roleOf answers the only question authorization actually asks: what is THIS
// user's role in this chat, if any. Small chats answer from the cached map;
// large ones from a primary-key probe, memoized for the same TTL so a member who
// sends several messages does not pay for several lookups.
func (s *Service) roleOf(ctx context.Context, chatID, userID string) (model.ChatType, model.MemberRole, bool, error) {
	e, err := s.authView(ctx, chatID)
	if err != nil {
		return "", "", false, err
	}
	if !e.large {
		role, ok := e.roles[userID]
		return e.typ, role, ok, nil
	}

	key := chatID + "|" + userID
	s.mu.RLock()
	mr, ok := s.roleCache[key]
	s.mu.RUnlock()
	if ok && time.Now().Before(mr.expires) {
		return e.typ, mr.role, mr.member, nil
	}

	m, err := s.chats.GetMember(ctx, chatID, userID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		mr = memberRole{expires: time.Now().Add(authCacheTTL)}
	case err != nil:
		return "", "", false, err
	default:
		mr = memberRole{role: m.Role, member: true, expires: time.Now().Add(authCacheTTL)}
	}
	s.mu.Lock()
	s.sweepLocked()
	s.roleCache[key] = mr
	s.mu.Unlock()
	return e.typ, mr.role, mr.member, nil
}

// sweepLocked collects expired entries, then enforces the size ceiling. Caller
// holds the write lock.
//
// Eviction under the ceiling is arbitrary (Go randomizes map iteration) rather
// than least-recently-used: with a 5-second TTL, a victim is re-read from the
// store almost as cheaply as it would have been on expiry, so the bookkeeping an
// LRU would need buys nothing. Being over the ceiling at all means the node is
// touching more chats than the cache was sized for — the right response is to
// stay bounded, not to choose cleverly.
func (s *Service) sweepLocked() {
	now := time.Now()
	if now.Sub(s.lastSweep) < authSweepEvery &&
		len(s.cache) < authCacheMax && len(s.roleCache) < authCacheMax {
		return
	}
	s.lastSweep = now
	for k, e := range s.cache {
		if now.After(e.expires) {
			delete(s.cache, k)
		}
	}
	for k := range s.cache {
		if len(s.cache) < authCacheMax {
			break
		}
		delete(s.cache, k)
	}
	for k, mr := range s.roleCache {
		if now.After(mr.expires) {
			delete(s.roleCache, k)
		}
	}
	for k := range s.roleCache {
		if len(s.roleCache) < authCacheMax {
			break
		}
		delete(s.roleCache, k)
	}
	metrics.CacheEntries.WithLabelValues("chat_auth").Set(float64(len(s.cache)))
	metrics.CacheEntries.WithLabelValues("chat_member_role").Set(float64(len(s.roleCache)))
}

// invalidate drops a chat's cached authorization view after a membership change.
func (s *Service) invalidate(chatID string) {
	s.mu.Lock()
	delete(s.cache, chatID)
	// Memoized per-user roles for this chat are part of the same view: a join or
	// a demotion has to take effect at once, not when each user's entry expires.
	prefix := chatID + "|"
	for k := range s.roleCache {
		if strings.HasPrefix(k, prefix) {
			delete(s.roleCache, k)
		}
	}
	s.mu.Unlock()
}

// EnsureDirect returns the canonical 1:1 chat between two users, creating it and
// seeding membership if it does not yet exist. Idempotent on the unordered pair.
func (s *Service) EnsureDirect(ctx context.Context, userA, userB string) (*model.Chat, error) {
	c, err := s.chats.GetOrCreateDirect(ctx, userA, userB, s.ids.NextString())
	if err == nil {
		s.invalidate(c.ID) // ensure freshly-seeded members are visible immediately
	}
	return c, err
}

// FindDirect returns the existing 1:1 chat for a pair, or store.ErrNotFound. It
// never creates — the gateway uses it to rate-limit only NEW conversations.
func (s *Service) FindDirect(ctx context.Context, userA, userB string) (*model.Chat, error) {
	return s.chats.GetDirect(ctx, userA, userB)
}

// CreateGroup creates a group (or channel) chat with an initial member set.
func (s *Service) CreateGroup(ctx context.Context, ownerID, title string, typ model.ChatType, members []string) (*model.Chat, error) {
	c := &model.Chat{
		ID:        s.ids.NextString(),
		Type:      typ,
		Title:     title,
		OwnerID:   ownerID,
		CreatedAt: nowMs(),
	}
	if err := s.chats.CreateChat(ctx, c); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	add := func(uid string, role model.MemberRole) error {
		if seen[uid] {
			return nil
		}
		seen[uid] = true
		return s.chats.AddMember(ctx, &model.ChatMember{
			ChatID: c.ID, UserID: uid, Role: role, JoinedAt: c.CreatedAt,
		})
	}
	if err := add(ownerID, model.RoleOwner); err != nil {
		return nil, err
	}
	for _, uid := range members {
		if err := add(uid, model.RoleMember); err != nil {
			return nil, err
		}
	}
	s.invalidate(c.ID)
	return c, nil
}

// Get returns a chat by id.
func (s *Service) Get(ctx context.Context, chatID string) (*model.Chat, error) {
	return s.chats.GetChat(ctx, chatID)
}

// Members lists a chat's members.
func (s *Service) Members(ctx context.Context, chatID string) ([]*model.ChatMember, error) {
	return s.chats.ListMembers(ctx, chatID)
}

// UserChats returns one page of the chats a user belongs to, ordered by chat id
// and starting after `after` ("" = from the beginning).
//
// The summary is assembled HERE rather than at the gateway. The membership rows
// hold ids only, so each row still costs a chat read and — for a 1:1, which has
// no title — a membership read to find the peer; done at the edge in the split
// deployment that is two RPCs per row, so a single screen of chats becomes a
// burst of round trips. Paging by id (not by offset) keeps a page stable while
// the list changes underneath it.
//
// A chat that disappears between the membership read and its own is skipped
// rather than failing the page: the list is a view, and one missing row must
// not cost the client the other ninety-nine.
func (s *Service) UserChats(ctx context.Context, userID, after string, limit int) ([]model.ChatSummary, error) {
	if limit <= 0 || limit > maxChatPage {
		limit = defaultChatPage
	}
	ids, err := s.chats.ListUserChats(ctx, userID)
	if err != nil {
		return nil, err
	}
	sort.Slice(ids, func(i, j int) bool { return lessID(ids[i], ids[j]) })

	out := make([]model.ChatSummary, 0, limit)
	for _, id := range ids {
		if after != "" && !lessID(after, id) {
			continue
		}
		if len(out) == limit {
			break
		}
		c, err := s.chats.GetChat(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sum := model.ChatSummary{Chat: c}
		// A membership PROBE, not the cached authorization view: the view loads a
		// chat's whole role map, so building a page through it would read up to
		// maxCachedMembers rows per row of the list — and every question here is
		// about one user in one chat, which is what a primary-key probe answers.
		if m, err := s.chats.GetMember(ctx, id, userID); err == nil {
			sum.MyRole = m.Role
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if c.Type == model.ChatDirect {
			members, err := s.chats.ListMembers(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, m := range members {
				if m.UserID != userID {
					sum.PeerID = m.UserID
					break
				}
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

// MemberIDs returns a chat's member ids. It is for chats small enough to handle
// as a whole — callers that may face a huge one (fanout) walk MemberIDsPage
// instead. For a large chat it pages rather than reading a cache it no longer
// keeps, so the answer stays correct; it is simply not the cheap call there.
func (s *Service) MemberIDs(ctx context.Context, chatID string) ([]string, error) {
	e, err := s.authView(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if !e.large {
		out := make([]string, 0, len(e.roles))
		for uid := range e.roles {
			out = append(out, uid)
		}
		return out, nil
	}
	var out []string
	after := ""
	for {
		page, err := s.chats.ListMemberIDsPage(ctx, chatID, after, 500)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		out = append(out, page...)
		after = page[len(page)-1]
	}
}

// MemberIDsPage walks a chat's members by keyset, bypassing the authorization
// cache. The cache holds a chat's WHOLE role map, which is the right shape for
// answering "may this user post?" and the wrong one for a channel with a million
// members — fanout needs to stream them, not materialize them.
func (s *Service) MemberIDsPage(ctx context.Context, chatID, afterUserID string, limit int) ([]string, error) {
	return s.chats.ListMemberIDsPage(ctx, chatID, afterUserID, limit)
}

// CanPost reports whether a user may post to a chat (cached authz). For channels
// only admins and the owner may post; for direct/group any member may.
func (s *Service) CanPost(ctx context.Context, chatID, userID string) (bool, error) {
	typ, role, member, err := s.roleOf(ctx, chatID, userID)
	if err != nil || !member {
		return false, err
	}
	if typ == model.ChatChannel {
		return role == model.RoleAdmin || role == model.RoleOwner, nil
	}
	return true, nil
}

// IsMember reports chat membership (cached read authorization).
func (s *Service) IsMember(ctx context.Context, chatID, userID string) (bool, error) {
	_, _, member, err := s.roleOf(ctx, chatID, userID)
	return member, err
}

// MemberRole returns a user's role in a chat (ok=false when not a member). It is
// the point-lookup shape the invite service needs: "what may this ONE person do
// here?" must not cost a walk of everyone who is here.
func (s *Service) MemberRole(ctx context.Context, chatID, userID string) (model.MemberRole, bool, error) {
	_, role, member, err := s.roleOf(ctx, chatID, userID)
	return role, member, err
}

// CountMembersWithRole counts holders of a role — "is this the last owner?"
// asked as a count instead of an enumeration.
func (s *Service) CountMembersWithRole(ctx context.Context, chatID string, role model.MemberRole) (int, error) {
	return s.chats.CountMembersWithRole(ctx, chatID, role)
}

// NextSeq atomically allocates the next per-chat ordering position.
func (s *Service) NextSeq(ctx context.Context, chatID string) (uint64, error) {
	return s.chats.BumpSeq(ctx, chatID)
}

// CanPin reports whether a user may pin/unpin in a chat. A pin is visible to
// everyone, so in groups and channels it is an ADMIN action; in a 1:1 chat both
// participants are equals and either may pin.
func (s *Service) CanPin(ctx context.Context, chatID, userID string) (bool, error) {
	typ, role, member, err := s.roleOf(ctx, chatID, userID)
	if err != nil || !member {
		return false, err
	}
	if typ == model.ChatDirect {
		return true, nil
	}
	return role == model.RoleAdmin || role == model.RoleOwner, nil
}

// AddMember adds a user to a chat and invalidates the cached authorization view,
// so a member who just joined via an invite can post immediately instead of
// waiting for the cache TTL.
func (s *Service) AddMember(ctx context.Context, m *model.ChatMember) error {
	if err := s.chats.AddMember(ctx, m); err != nil {
		return err
	}
	s.invalidate(m.ChatID)
	return nil
}

// SetMemberRole changes a member's role and invalidates the cached view, so a
// demotion takes effect at once rather than lingering for the cache TTL.
func (s *Service) SetMemberRole(ctx context.Context, chatID, userID string, role model.MemberRole) error {
	rs, ok := s.chats.(store.MemberRoleStore)
	if !ok {
		return store.ErrNotFound
	}
	if err := rs.SetMemberRole(ctx, chatID, userID, role); err != nil {
		return err
	}
	s.invalidate(chatID)
	return nil
}
