package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
)

type failingRoles struct{ store.PlatformRoleStore }

func (failingRoles) PlatformRole(context.Context, string) (model.PlatformRole, error) {
	return "", errors.New("database unavailable")
}

func TestRolesComeFromConfigAndStore(t *testing.T) {
	ctx := context.Background()
	st := memory.New().Stores()
	for _, u := range []*model.User{{ID: "1", Username: "admin"}, {ID: "2", Username: "mod"}, {ID: "3", Username: "user"}} {
		if err := st.Users.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Roles.SetPlatformRole(ctx, model.PlatformRoleGrant{UserID: "2", Role: model.PlatformModerator, GrantedBy: "1", GrantedAt: 1}); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.AdminUsers = []string{"1"}
	g := New(Services{Roles: st.Roles}, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !g.canExportAny(ctx, "1") {
		t.Fatal("a configured admin must be able to export")
	}
	if !g.canExportAny(ctx, "2") {
		t.Fatal("a moderator granted in the store must be able to export")
	}
	if g.canExportAny(ctx, "3") {
		t.Fatal("a user with no role must not export other people's chats")
	}

	// A revocation reaches the node once the cached entry expires.
	if err := st.Roles.RemovePlatformRole(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	g.roleCache.mu.Lock()
	e := g.roleCache.entries["2"]
	e.expires = time.Now().Add(-time.Second)
	g.roleCache.entries["2"] = e
	g.roleCache.mu.Unlock()
	if g.canExportAny(ctx, "2") {
		t.Fatal("a revoked moderator kept the role past the cache TTL")
	}
}

// A privilege check that cannot reach the store answers "no".
func TestARoleLookupFailureGrantsNothing(t *testing.T) {
	g := New(Services{Roles: failingRoles{}}, DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if g.canExportAny(context.Background(), "7") {
		t.Fatal("a failed lookup granted a role")
	}
}
