package auth

import (
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
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
