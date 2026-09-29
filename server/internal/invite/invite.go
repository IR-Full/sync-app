// Package invite implements the two ways to reach a chat you are not in —
// PUBLIC HANDLES (a channel anyone can find) and INVITE LINKS (a private chat
// you can be let into) — plus the admin-rights changes that gate both.
//
// The security shape differs sharply between them:
//
//	A handle is public by intent: knowing it grants no membership, only
//	discoverability, so it can be a short human-readable name.
//
//	A link IS the credential: anyone holding it can join, so the code must be
//	unguessable, and it must be revocable and boundable (uses/expiry). An
//	unbounded, unrevocable link is a permanent hole in a private chat.
package invite

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/tracing"
)

var (
	// ErrForbidden means the actor lacks the required chat rights.
	ErrForbidden = errors.New("invite: forbidden")
	// ErrBadUsername means the handle failed validation.
	ErrBadUsername = errors.New("invite: invalid username")
	// ErrTaken means the handle is already claimed.
	ErrTaken = errors.New("invite: username taken")
	// ErrInvalidLink means the link is missing, revoked, expired, or exhausted.
	ErrInvalidLink = errors.New("invite: link is not usable")
	// ErrLastOwner means demoting this user would leave the chat ownerless.
	ErrLastOwner = errors.New("invite: cannot demote the last owner")
)

// usernameRe bounds handles to a safe, unambiguous alphabet: mixing scripts or
// punctuation into public handles invites look-alike impersonation.
var usernameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{4,31}$`)

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

// New builds the invite service.
func New(st store.InviteStore, roles store.MemberRoleStore, chats Chats) *Service {
	return &Service{store: st, roles: roles, chats: chats}
}

// SetUsername claims (or clears with "") a chat's public handle. Owner-only:
// the handle is the chat's public identity, not a per-admin setting.
func (s *Service) SetUsername(ctx context.Context, chatID, actorID, username string) error {
	ctx, span := tracing.Start(ctx, "invite.SetUsername")
	defer span.End()

	if err := s.requireRole(ctx, chatID, actorID, model.RoleOwner); err != nil {
		return err
	}
	if username != "" && !usernameRe.MatchString(username) {
		return ErrBadUsername
	}
	err := s.store.SetChatUsername(ctx, chatID, username)
	if errors.Is(err, store.ErrConflict) {
		return ErrTaken
	}
	return err
}

// ResolveUsername finds a chat by its public handle.
func (s *Service) ResolveUsername(ctx context.Context, username string) (*model.Chat, error) {
	return s.store.GetChatByUsername(ctx, strings.TrimPrefix(username, "@"))
}

// CreateLink mints an invite link. Admins and the owner may create them.
// expiresAt/maxUses of 0 mean unlimited — allowed, but the caller is choosing
// that explicitly rather than getting it by default in the protocol.
func (s *Service) CreateLink(ctx context.Context, chatID, actorID string, expiresAt int64, maxUses int32, now int64) (*model.InviteLink, error) {
	if err := s.requireRole(ctx, chatID, actorID, model.RoleAdmin); err != nil {
		return nil, err
	}
	code, err := newCode()
	if err != nil {
		return nil, err
	}
	l := &model.InviteLink{
		Code: code, ChatID: chatID, CreatedBy: actorID, CreatedAt: now,
		ExpiresAt: expiresAt, MaxUses: maxUses,
	}
	if err := s.store.CreateInvite(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// RevokeLink kills a link immediately (admin/owner).
func (s *Service) RevokeLink(ctx context.Context, chatID, actorID, code string) error {
	if err := s.requireRole(ctx, chatID, actorID, model.RoleAdmin); err != nil {
		return err
	}
	return s.store.RevokeInvite(ctx, code, chatID)
}

// ListLinks returns a chat's live links (admin/owner).
func (s *Service) ListLinks(ctx context.Context, chatID, actorID string) ([]*model.InviteLink, error) {
	if err := s.requireRole(ctx, chatID, actorID, model.RoleAdmin); err != nil {
		return nil, err
	}
	return s.store.ListInvites(ctx, chatID)
}

// Join redeems a link and adds the user. The redemption is atomic in the store,
// so a link capped at N uses cannot be over-redeemed by concurrent joins.
// Re-using a link you already redeemed does NOT consume another use.
func (s *Service) Join(ctx context.Context, code, userID string, now int64) (*model.Chat, error) {
	ctx, span := tracing.Start(ctx, "invite.Join")
	defer span.End()

	l, err := s.store.GetInvite(ctx, code)
	if err != nil {
		return nil, ErrInvalidLink
	}
	if member, err := s.chats.IsMember(ctx, l.ChatID, userID); err != nil {
		return nil, err
	} else if member {
		return s.chats.Get(ctx, l.ChatID) // already in — idempotent, no use spent
	}
	used, err := s.store.UseInvite(ctx, code, now)
	if err != nil {
		return nil, ErrInvalidLink
	}
	if err := s.chats.AddMember(ctx, &model.ChatMember{
		ChatID: used.ChatID, UserID: userID, Role: model.RoleMember, JoinedAt: now,
	}); err != nil {
		return nil, err
	}
	return s.chats.Get(ctx, used.ChatID)
}

// JoinPublic joins a chat by its public handle (channels/groups that chose to be
// discoverable). No link needed — that is what "public" means.
func (s *Service) JoinPublic(ctx context.Context, username, userID string, now int64) (*model.Chat, error) {
	c, err := s.ResolveUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if member, err := s.chats.IsMember(ctx, c.ID, userID); err != nil {
		return nil, err
	} else if member {
		return c, nil
	}
	if err := s.chats.AddMember(ctx, &model.ChatMember{
		ChatID: c.ID, UserID: userID, Role: model.RoleMember, JoinedAt: now,
	}); err != nil {
		return nil, err
	}
	return c, nil
}

// SetRole promotes or demotes a member. Only the OWNER may change roles —
// otherwise an admin could demote the owner and take the chat. Demoting the last
// owner is refused so a chat can never become unadministrable.
func (s *Service) SetRole(ctx context.Context, chatID, actorID, targetID string, role model.MemberRole) error {
	if err := s.requireRole(ctx, chatID, actorID, model.RoleOwner); err != nil {
		return err
	}
	if role != model.RoleMember && role != model.RoleAdmin && role != model.RoleOwner {
		return ErrForbidden
	}
	if role != model.RoleOwner {
		// Only the target's own role and the owner COUNT matter here; who else
		// holds the role does not, so neither should the cost.
		targetRole, member, err := s.chats.MemberRole(ctx, chatID, targetID)
		if err != nil {
			return err
		}
		if !member {
			return ErrForbidden
		}
		if targetRole == model.RoleOwner {
			owners, err := s.chats.CountMembersWithRole(ctx, chatID, model.RoleOwner)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
	}
	return s.roles.SetMemberRole(ctx, chatID, targetID, role)
}

// requireRole checks the actor holds at least the given rank in the chat.
func (s *Service) requireRole(ctx context.Context, chatID, actorID string, min model.MemberRole) error {
	role, member, err := s.chats.MemberRole(ctx, chatID, actorID)
	if err != nil {
		return err
	}
	if !member || rank(role) < rank(min) {
		return ErrForbidden
	}
	return nil
}

// rank orders roles so "at least admin" is a comparison, not a set membership
// test that has to enumerate every superior role.
func rank(r model.MemberRole) int {
	switch r {
	case model.RoleOwner:
		return 3
	case model.RoleAdmin:
		return 2
	case model.RoleMember:
		return 1
	}
	return 0
}

// newCode mints an unguessable link token. The code IS the credential, so it
// carries 128 bits of entropy — a short/sequential code would be brute-forceable
// into private chats.
func newCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
