package chat

import (
	"sync"
	"time"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
	"github.com/IR-Full/sync-app/server/pkg/id"
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

// ChatPage is a chat-list request.
//
// The cursor is (AfterActivity, After) — the last row of the previous page —
// rather than a bare chat id. A list ordered by activity reorders as messages
// arrive, so a cursor that named only a position would skip and repeat rows
// exactly when the chat is busy; naming the row's sort key AND its id gives a
// total order the walk cannot fall out of.
type ChatPage struct {
	// AfterActivity is the previous page's last LastActivityAt (0 = first page).
	AfterActivity int64
	// After is the previous page's last chat id, breaking ties on AfterActivity.
	After string
	Limit int
	// IncludeArchived lists the archived pile instead of hiding it. Archiving is a
	// per-member flag, so this filters the caller's own rows rather than selecting
	// a different set of chats.
	IncludeArchived bool
}
