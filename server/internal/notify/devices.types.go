package notify

import "github.com/synapse-chat/synapse/internal/store"

// StoreDevices adapts the user store to the push path's device lookup. It lives
// here rather than in the store so the notification service depends on the two
// operations it actually needs — list a user's tokens, forget a dead one — and
// not on the whole user aggregate.
type StoreDevices struct{ Users store.UserStore }
