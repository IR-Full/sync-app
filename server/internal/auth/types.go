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
}

// Identity is the resolved principal behind a validated session.
type Identity struct {
	Session *model.Session
	User    *model.User
}
