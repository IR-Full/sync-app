package auth

import (
	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/pkg/id"
)

// Service implements identity and session management.
type Service struct {
	users    store.UserStore
	sessions store.SessionStore
	ids      *id.Generator
	// twoFactor is optional. A deployment without it has no second factor —
	// which is where the system was — rather than a broken one.
	twoFactor store.TwoFactorStore
}

// Identity is the resolved principal behind a validated session.
type Identity struct {
	Session *model.Session
	User    *model.User
}
