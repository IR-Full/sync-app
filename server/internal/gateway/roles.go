package gateway

import (
	"context"
	"sync"
	"time"
)

// roleCacheTTL bounds how long a grant or a revocation takes to reach a node.
// Role checks sit on rare, privileged actions, so a short TTL costs little and
// keeps a revoked moderator from exporting chats for longer than this.
const roleCacheTTL = 30 * time.Second

// roleCacheMax caps the cache; past it the cache is dropped and refilled.
const roleCacheMax = 10_000

type roleEntry struct {
	role    Role
	expires time.Time
}

// roleCache remembers platform roles read from the store.
type roleCache struct {
	mu      sync.Mutex
	entries map[string]roleEntry
}

// roleOf resolves a user's platform role: a role from the configuration first,
// then one granted in the store. A store error resolves to no role — failing
// closed on a privilege check — and is not cached.
func (g *Gateway) roleOf(ctx context.Context, userID string) Role {
	if r, ok := g.roles[userID]; ok {
		return r
	}
	if g.svc.Roles == nil {
		return ""
	}
	now := time.Now()
	g.roleCache.mu.Lock()
	if e, ok := g.roleCache.entries[userID]; ok && now.Before(e.expires) {
		g.roleCache.mu.Unlock()
		return e.role
	}
	g.roleCache.mu.Unlock()

	role, err := g.svc.Roles.PlatformRole(ctx, userID)
	if err != nil {
		g.log.Warn("platform role lookup failed", "user", logUser(userID), "err", err)
		return ""
	}
	g.roleCache.mu.Lock()
	defer g.roleCache.mu.Unlock()
	if g.roleCache.entries == nil || len(g.roleCache.entries) >= roleCacheMax {
		g.roleCache.entries = map[string]roleEntry{}
	}
	g.roleCache.entries[userID] = roleEntry{role: Role(role), expires: now.Add(roleCacheTTL)}
	return Role(role)
}
