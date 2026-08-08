package chat

import (
	"sync"
	"time"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
	"github.com/synapse-chat/synapse/pkg/id"
)

// Service manages chats and membership.
type Service struct {
	chats store.ChatStore
	ids   *id.Generator

	mu    sync.RWMutex
	cache map[string]*authEntry // chatID -> cached authorization view
	// roleCache memoizes single (chat, user) roles for chats too large to hold
	// whole. Keyed "chatID|userID"; swept with the same janitor.
	roleCache map[string]memberRole
	lastSweep time.Time // last expiry collection (see authSweepEvery)
}

// authEntry is a chat's cached authorization data (never the mutable LastSeq).
// roles is nil when the chat is too large to hold whole; the type is always
// cached, because it is one word and every authorization question needs it.
type authEntry struct {
	typ     model.ChatType
	roles   map[string]model.MemberRole // userID -> role (nil when large)
	large   bool
	expires time.Time
}

// memberRole is one memoized (chat, user) role for a chat too large to cache.
type memberRole struct {
	role    model.MemberRole
	member  bool
	expires time.Time
}
