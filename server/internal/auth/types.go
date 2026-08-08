package auth

import (
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/id"
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
