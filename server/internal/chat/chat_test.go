package chat

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
)

// TestAuthCacheIsBounded pins the two ways the authorization cache is kept from
// growing forever: expired views are collected, and the ceiling holds even when
// nothing has expired. This cache is the more expensive of the two in the system
// — every entry carries the chat's whole role map — so an entry that is never
// read again but never dropped is a leak proportional to the chats the node has
// ever authorized.
func TestAuthCacheIsBounded(t *testing.T) {
	s := New(nil, nil) // the sweep touches only the cache, never the store

	s.cache["expired"] = &authEntry{
		typ: model.ChatDirect, roles: map[string]model.MemberRole{"u1": model.RoleOwner},
		expires: time.Now().Add(-time.Minute),
	}
	s.cache["live"] = &authEntry{
		typ: model.ChatGroup, roles: map[string]model.MemberRole{"u2": model.RoleMember},
		expires: time.Now().Add(time.Minute),
	}
	s.lastSweep = time.Now().Add(-2 * authSweepEvery)

	s.mu.Lock()
	s.sweepLocked()
	s.mu.Unlock()

	if _, ok := s.cache["expired"]; ok {
		t.Fatal("expired view survived the sweep")
	}
	if _, ok := s.cache["live"]; !ok {
		t.Fatal("live view evicted by the sweep")
	}

	// Nothing below has expired, so only the ceiling can bound this.
	for i := 0; i < authCacheMax+100; i++ {
		s.cache[fmt.Sprintf("chat%d", i)] = &authEntry{expires: time.Now().Add(time.Minute)}
	}
	s.mu.Lock()
	s.sweepLocked()
	s.mu.Unlock()
	if len(s.cache) >= authCacheMax {
		t.Fatalf("cache stayed above its ceiling: %d entries", len(s.cache))
	}
}

// countingChats is a store that reports how much membership each call asked for,
// and refuses to hand out more than one page — the shape a million-member channel
// has in production.
type countingChats struct {
	store.ChatStore
	typ         model.ChatType
	members     map[string]model.MemberRole
	maxAsked    int
	pointLookup int
}

func (c *countingChats) GetChat(context.Context, string) (*model.Chat, error) {
	return &model.Chat{ID: "big", Type: c.typ}, nil
}

func (c *countingChats) ListMembersPage(_ context.Context, _, after string, limit int) ([]*model.ChatMember, error) {
	if limit > c.maxAsked {
		c.maxAsked = limit
	}
	ids := make([]string, 0, len(c.members))
	for uid := range c.members {
		if uid > after {
			ids = append(ids, uid)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]*model.ChatMember, 0, len(ids))
	for _, uid := range ids {
		out = append(out, &model.ChatMember{ChatID: "big", UserID: uid, Role: c.members[uid]})
	}
	return out, nil
}

func (c *countingChats) GetMember(_ context.Context, _, userID string) (*model.ChatMember, error) {
	c.pointLookup++
	role, ok := c.members[userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &model.ChatMember{ChatID: "big", UserID: userID, Role: role}, nil
}

// TestHugeChatIsAuthorizedWithoutMaterializing pins the shape of authorization at
// scale. Every question the gateway asks — may this user post, is this user a
// member, may this user pin — is about ONE user, so answering it must not depend
// on how many other people are in the chat. Below the threshold the whole role
// map is still cached, because for an ordinary chat that is the cheaper answer.
func TestHugeChatIsAuthorizedWithoutMaterializing(t *testing.T) {
	ctx := context.Background()
	members := map[string]model.MemberRole{}
	for i := 0; i < maxCachedMembers*3; i++ {
		members[fmt.Sprintf("u%07d", i)] = model.RoleMember
	}
	members["u0000000"] = model.RoleOwner
	cc := &countingChats{typ: model.ChatChannel, members: members}
	s := New(cc, nil)

	// A member of a huge CHANNEL may read but not post; the owner may post.
	if ok, err := s.IsMember(ctx, "big", "u0000005"); err != nil || !ok {
		t.Fatalf("member not recognized in a huge chat: ok=%v err=%v", ok, err)
	}
	if ok, err := s.CanPost(ctx, "big", "u0000005"); err != nil || ok {
		t.Fatalf("an ordinary member may post to a channel: ok=%v err=%v", ok, err)
	}
	if ok, err := s.CanPost(ctx, "big", "u0000000"); err != nil || !ok {
		t.Fatalf("the owner may not post to their own channel: ok=%v err=%v", ok, err)
	}
	if ok, err := s.IsMember(ctx, "big", "stranger"); err != nil || ok {
		t.Fatalf("a stranger was treated as a member: ok=%v err=%v", ok, err)
	}

	if cc.maxAsked > maxCachedMembers+1 {
		t.Fatalf("authorization asked for %d members — it is materializing the chat", cc.maxAsked)
	}
	s.mu.RLock()
	entry := s.cache["big"]
	roles := len(s.roleCache)
	s.mu.RUnlock()
	if entry == nil || !entry.large || entry.roles != nil {
		t.Fatalf("a huge chat was cached as a whole role map: %+v", entry)
	}
	if roles == 0 {
		t.Fatal("per-user roles were not memoized; every question re-queries")
	}

	// Repeating a question must not repeat the lookup.
	before := cc.pointLookup
	for i := 0; i < 5; i++ {
		if _, err := s.CanPost(ctx, "big", "u0000000"); err != nil {
			t.Fatal(err)
		}
	}
	if cc.pointLookup != before {
		t.Fatalf("memoization is not working: %d extra lookups", cc.pointLookup-before)
	}

	// A membership change must take effect at once, not when each entry expires.
	s.invalidate("big")
	s.mu.RLock()
	left := len(s.roleCache)
	s.mu.RUnlock()
	if left != 0 {
		t.Fatalf("invalidate left %d memoized roles behind", left)
	}
}

// TestOrdinaryChatKeepsTheWholeRoleMap guards the fast path: the cache exists to
// answer every member's question from one query, and a small chat must keep that.
func TestOrdinaryChatKeepsTheWholeRoleMap(t *testing.T) {
	ctx := context.Background()
	cc := &countingChats{typ: model.ChatGroup, members: map[string]model.MemberRole{
		"u1": model.RoleOwner, "u2": model.RoleMember,
	}}
	s := New(cc, nil)

	if ok, err := s.CanPost(ctx, "big", "u2"); err != nil || !ok {
		t.Fatalf("a group member may not post: ok=%v err=%v", ok, err)
	}
	if cc.pointLookup != 0 {
		t.Fatalf("a small chat fell back to per-user lookups (%d)", cc.pointLookup)
	}
	s.mu.RLock()
	entry := s.cache["big"]
	s.mu.RUnlock()
	if entry == nil || entry.large || len(entry.roles) != 2 {
		t.Fatalf("small chat not cached as a whole: %+v", entry)
	}
}
