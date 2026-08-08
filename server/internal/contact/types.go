package contact

import (
	"context"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
)

// Users resolves user existence (and @username lookups).
type Users interface {
	GetUser(ctx context.Context, id string) (*model.User, error)
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
}

// Service manages address books and block lists.
type Service struct {
	store store.ContactStore
	users Users
}
