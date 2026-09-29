package contact

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
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
