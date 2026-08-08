package invite

import (
	"context"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
)

// Chats supplies role information and membership mutation.
type Chats interface {
	Get(ctx context.Context, chatID string) (*model.Chat, error)
	AddMember(ctx context.Context, m *model.ChatMember) error
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
	// MemberRole answers "what may this ONE person do here?". Rights checks used
	// to list every member and scan for the actor, which made the cost of an admin
	// action depend on how popular the chat is — worst in exactly the channels
	// where admin actions matter most.
	MemberRole(ctx context.Context, chatID, userID string) (model.MemberRole, bool, error)
	// CountMembersWithRole makes "is this the last owner?" a count instead of an
	// enumeration.
	CountMembersWithRole(ctx context.Context, chatID string, role model.MemberRole) (int, error)
}

// Service manages handles, links, and admin rights.
type Service struct {
	store store.InviteStore
	roles store.MemberRoleStore
	chats Chats
}
