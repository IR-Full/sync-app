package rpc

import (
	"context"
	"log/slog"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/keydir"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	pb "github.com/SyncApp-chat/SyncApp/internal/rpc/pb"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
	"google.golang.org/grpc"
)

// The client adapters below satisfy the gateway's service interfaces
// (internal/gateway/services.go) and the message/search chat dependency and
// keydir.Directory. Domain sentinel errors the gateway checks with errors.Is
// (store.ErrNotFound, message.ErrForbidden/ErrEmptyMessage/…) are mapped to gRPC
// status codes on the server and back to the sentinels here (see errors.go), so
// the split behaves identically to the monolith — e.g. FindDirect returning
// "not found" still falls through to EnsureDirect.

// ---- Auth ----

// NewAuthClient builds an auth gRPC client over conn.
func NewAuthClient(conn grpc.ClientConnInterface) *AuthClient {
	return &AuthClient{c: pb.NewAuthServiceClient(conn)}
}

func (a *AuthClient) Register(ctx context.Context, username, password, displayName, deviceID, platform string) (*model.Session, *model.User, error) {
	r, err := a.c.Register(ctx, &pb.RegisterRequest{Username: username, Password: password, DisplayName: displayName, DeviceId: deviceID, Platform: platform})
	if err != nil {
		return nil, nil, fromStatus(err)
	}
	return modelSession(r.Session), modelUser(r.User), nil
}

func (a *AuthClient) Login(ctx context.Context, username, password, deviceID, platform string) (*model.Session, *model.User, error) {
	r, err := a.c.Login(ctx, &pb.LoginRequest{Username: username, Password: password, DeviceId: deviceID, Platform: platform})
	if err != nil {
		return nil, nil, fromStatus(err)
	}
	return modelSession(r.Session), modelUser(r.User), nil
}

// LoginWithCode is Login plus a second factor.
func (a *AuthClient) LoginWithCode(ctx context.Context, username, password, code, deviceID, platform string) (*model.Session, *model.User, error) {
	r, err := a.c.LoginWithCode(ctx, &pb.LoginWithCodeRequest{
		Username: username, Password: password, Code: code,
		DeviceId: deviceID, Platform: platform,
	})
	if err != nil {
		return nil, nil, fromStatus(err)
	}
	return modelSession(r.Session), modelUser(r.User), nil
}

// ChangePassword replaces the password and revokes every other session.
func (a *AuthClient) ChangePassword(ctx context.Context, userID, oldPassword, newPassword, keepSessionID string) (int, error) {
	r, err := a.c.ChangePassword(ctx, &pb.ChangePasswordRequest{
		UserId: userID, OldPassword: oldPassword, NewPassword: newPassword,
		KeepSessionId: keepSessionID,
	})
	if err != nil {
		return 0, fromStatus(err)
	}
	return int(r.SessionsRevoked), nil
}

func (a *AuthClient) BeginTOTP(ctx context.Context, userID, issuer string) (string, string, error) {
	r, err := a.c.BeginTOTP(ctx, &pb.BeginTOTPRequest{UserId: userID, Issuer: issuer})
	if err != nil {
		return "", "", fromStatus(err)
	}
	return r.Secret, r.Uri, nil
}

func (a *AuthClient) ConfirmTOTP(ctx context.Context, userID, code string) ([]string, error) {
	r, err := a.c.ConfirmTOTP(ctx, &pb.ConfirmTOTPRequest{UserId: userID, Code: code})
	if err != nil {
		return nil, fromStatus(err)
	}
	return r.RecoveryCodes, nil
}

func (a *AuthClient) DisableTOTP(ctx context.Context, userID, password, code string) error {
	_, err := a.c.DisableTOTP(ctx, &pb.DisableTOTPRequest{UserId: userID, Password: password, Code: code})
	if err != nil {
		return fromStatus(err)
	}
	return nil
}

// TwoFactorEnabled and RecoveryCodesLeft share one RPC.
//
// They return plain values with no error because the interface they satisfy does:
// the login path asks "is a code required" on every password login, and a
// transport hiccup answering that must not become a login failure. Reporting
// "not enabled" on an error is the wrong direction for security, so the tradeoff
// is stated here rather than hidden: an unreachable auth service degrades to
// single-factor, exactly as it did before the factor existed.
func (a *AuthClient) TwoFactorEnabled(ctx context.Context, userID string) bool {
	r, err := a.c.TwoFactorState(ctx, &pb.UserIDRequest{UserId: userID})
	return err == nil && r.Enabled
}

func (a *AuthClient) RecoveryCodesLeft(ctx context.Context, userID string) int {
	r, err := a.c.TwoFactorState(ctx, &pb.UserIDRequest{UserId: userID})
	if err != nil {
		return 0
	}
	return int(r.RecoveryLeft)
}

func (a *AuthClient) Authenticate(ctx context.Context, token string) (*auth.Identity, error) {
	r, err := a.c.Authenticate(ctx, &pb.TokenRequest{Token: token})
	if err != nil {
		return nil, fromStatus(err)
	}
	return &auth.Identity{Session: modelSession(r.Session), User: modelUser(r.User)}, nil
}

func (a *AuthClient) Resume(ctx context.Context, resumeToken string) (*auth.Identity, error) {
	r, err := a.c.Resume(ctx, &pb.ResumeRequest{ResumeToken: resumeToken})
	if err != nil {
		return nil, fromStatus(err)
	}
	return &auth.Identity{Session: modelSession(r.Session), User: modelUser(r.User)}, nil
}

// ---- Chat ----

// NewChatClient builds a chat gRPC client over conn.
func NewChatClient(conn grpc.ClientConnInterface) *ChatClient {
	return &ChatClient{c: pb.NewChatServiceClient(conn)}
}

func (c *ChatClient) EnsureDirect(ctx context.Context, userA, userB string) (*model.Chat, error) {
	r, err := c.c.EnsureDirect(ctx, &pb.DirectRequest{UserA: userA, UserB: userB})
	if err != nil {
		return nil, fromStatus(err)
	}
	return modelChat(r), nil
}

// EnsureSecret returns the canonical SECRET chat for a pair. It coexists with the
// ordinary direct chat with that person.
func (c *ChatClient) EnsureSecret(ctx context.Context, userA, userB string) (*model.Chat, error) {
	r, err := c.c.EnsureSecret(ctx, &pb.DirectRequest{UserA: userA, UserB: userB})
	if err != nil {
		return nil, fromStatus(err)
	}
	return modelChat(r), nil
}

func (c *ChatClient) FindDirect(ctx context.Context, userA, userB string) (*model.Chat, error) {
	r, err := c.c.FindDirect(ctx, &pb.DirectRequest{UserA: userA, UserB: userB})
	if err != nil {
		return nil, fromStatus(err)
	}
	return modelChat(r), nil
}

func (c *ChatClient) CreateGroup(ctx context.Context, ownerID, title string, typ model.ChatType, members []string) (*model.Chat, error) {
	r, err := c.c.CreateGroup(ctx, &pb.CreateGroupRequest{
		OwnerId: ownerID, Title: title, Type: string(typ), MemberIds: members,
	})
	if err != nil {
		return nil, fromStatus(err)
	}
	return modelChat(r), nil
}

func (c *ChatClient) Get(ctx context.Context, chatID string) (*model.Chat, error) {
	r, err := c.c.Get(ctx, &pb.ChatIDRequest{ChatId: chatID})
	if err != nil {
		return nil, fromStatus(err)
	}
	return modelChat(r), nil
}

func (c *ChatClient) Members(ctx context.Context, chatID string) ([]*model.ChatMember, error) {
	r, err := c.c.Members(ctx, &pb.ChatIDRequest{ChatId: chatID})
	if err != nil {
		return nil, fromStatus(err)
	}
	out := make([]*model.ChatMember, len(r.Members))
	for i, m := range r.Members {
		out[i] = modelMember(m)
	}
	return out, nil
}

func (c *ChatClient) UserChats(ctx context.Context, userID, after string, limit int) ([]model.ChatSummary, error) {
	r, err := c.c.UserChats(ctx, &pb.UserChatsRequest{UserId: userID, After: after, Limit: int32(limit)})
	if err != nil {
		return nil, fromStatus(err)
	}
	out := make([]model.ChatSummary, len(r.Chats))
	for i, s := range r.Chats {
		out[i] = modelChatSummary(s)
	}
	return out, nil
}

// UserChatPage is UserChats with the composite cursor and the archived filter.
func (c *ChatClient) UserChatPage(ctx context.Context, userID string, p chat.ChatPage) ([]model.ChatSummary, error) {
	r, err := c.c.UserChatPage(ctx, &pb.UserChatPageRequest{
		UserId: userID, After: p.After, AfterActivity: p.AfterActivity,
		Limit: int32(p.Limit), IncludeArchived: p.IncludeArchived,
	})
	if err != nil {
		return nil, fromStatus(err)
	}
	out := make([]model.ChatSummary, len(r.Chats))
	for i, s := range r.Chats {
		out[i] = modelChatSummary(s)
	}
	return out, nil
}

// SetChatFlags writes the caller's own mute/pin/archive for a chat.
func (c *ChatClient) SetChatFlags(ctx context.Context, chatID, userID string, f model.MemberFlags) (model.MemberFlags, error) {
	r, err := c.c.SetChatFlags(ctx, &pb.SetChatFlagsRequest{
		ChatId: chatID, UserId: userID,
		MutedUntil: f.MutedUntil, Pinned: f.Pinned, Archived: f.Archived,
	})
	if err != nil {
		return model.MemberFlags{}, fromStatus(err)
	}
	return model.MemberFlags{MutedUntil: r.MutedUntil, Pinned: r.Pinned, Archived: r.Archived}, nil
}

func (c *ChatClient) MemberIDs(ctx context.Context, chatID string) ([]string, error) {
	r, err := c.c.MemberIDs(ctx, &pb.ChatIDRequest{ChatId: chatID})
	if err != nil {
		return nil, fromStatus(err)
	}
	return r.UserIds, nil
}

func (c *ChatClient) MemberIDsPage(ctx context.Context, chatID, afterUserID string, limit int) ([]string, error) {
	r, err := c.c.MemberIDsPage(ctx, &pb.MemberPageRequest{ChatId: chatID, AfterUserId: afterUserID, Limit: int32(limit)})
	if err != nil {
		return nil, fromStatus(err)
	}
	return r.UserIds, nil
}

func (c *ChatClient) CanPost(ctx context.Context, chatID, userID string) (bool, error) {
	r, err := c.c.CanPost(ctx, &pb.ChatUserRequest{ChatId: chatID, UserId: userID})
	if err != nil {
		return false, fromStatus(err)
	}
	return r.Ok, nil
}

func (c *ChatClient) IsMember(ctx context.Context, chatID, userID string) (bool, error) {
	r, err := c.c.IsMember(ctx, &pb.ChatUserRequest{ChatId: chatID, UserId: userID})
	if err != nil {
		return false, fromStatus(err)
	}
	return r.Ok, nil
}

// CountPinnedChats counts the caller's pinned chats, for the MaxPinnedChats
// entitlement the gateway enforces.
func (c *ChatClient) CountPinnedChatsExcept(ctx context.Context, userID, exceptChatID string) (int, error) {
	r, err := c.c.CountPinnedChatsExcept(ctx, &pb.PinnedCountRequest{UserId: userID, ExceptChatId: exceptChatID})
	if err != nil {
		return 0, fromStatus(err)
	}
	return int(r.Count), nil
}

// CanModerate authorizes acting on another member's message. The error is
// returned rather than folded into a false, because the caller must be able to
// tell "chatd says no" from "chatd could not be reached" — the first is a
// refusal to show the user, the second a retry.
func (c *ChatClient) CanModerate(ctx context.Context, chatID, userID string) (bool, error) {
	r, err := c.c.CanModerate(ctx, &pb.ChatUserRequest{ChatId: chatID, UserId: userID})
	if err != nil {
		return false, fromStatus(err)
	}
	return r.Ok, nil
}

// UserChatIDs lists the chats a user belongs to, ids only.
//
// A dedicated RPC rather than paging UserChats: search needs the whole set at once
// to bound a query, and UserChats both pages and builds full summaries the caller
// discards. Bounded on the server side, which is where the bound belongs.
func (c *ChatClient) UserChatIDs(ctx context.Context, userID string) ([]string, error) {
	r, err := c.c.UserChatIDs(ctx, &pb.UserIDRequest{UserId: userID})
	if err != nil {
		return nil, fromStatus(err)
	}
	return r.UserIds, nil
}

// ---- Message ----

// NewMessageClient builds a message gRPC client over conn.
func NewMessageClient(conn grpc.ClientConnInterface) *MessageClient {
	return &MessageClient{c: pb.NewMessageServiceClient(conn)}
}

func (m *MessageClient) Submit(ctx context.Context, cmd message.Command) (message.Result, error) {
	r, err := m.c.Submit(ctx, &pb.SubmitRequest{
		Op: opToPB[cmd.Op], ActorId: cmd.ActorID, ChatId: cmd.ChatID, MessageId: cmd.MessageID,
		DedupKey: cmd.DedupKey, Text: cmd.Text, MediaRef: cmd.MediaRef, ReplyTo: cmd.ReplyTo,
		Attachment: pbAttachment(cmd.Attachment), TtlSeconds: cmd.TTLSeconds,
	})
	if err != nil {
		return message.Result{}, fromStatus(err)
	}
	return message.Result{Message: modelMessage(r.Message), Duplicate: r.Duplicate}, nil
}

func (m *MessageClient) History(ctx context.Context, userID, chatID string, beforeSeq uint64, limit int) ([]*model.Message, error) {
	r, err := m.c.History(ctx, &pb.HistoryRequest{UserId: userID, ChatId: chatID, BeforeSeq: beforeSeq, Limit: int32(limit)})
	if err != nil {
		return nil, fromStatus(err)
	}
	out := make([]*model.Message, len(r.Messages))
	for i, mm := range r.Messages {
		out[i] = modelMessage(mm)
	}
	return out, nil
}

func (m *MessageClient) MarkRead(ctx context.Context, userID, chatID string, upToSeq uint64) error {
	_, err := m.c.MarkRead(ctx, &pb.MarkReadRequest{UserId: userID, ChatId: chatID, UpToSeq: upToSeq})
	return fromStatus(err)
}

// ---- Presence ----

// NewPresenceClient builds a presence gRPC client over conn.
func NewPresenceClient(conn grpc.ClientConnInterface) *PresenceClient {
	return &PresenceClient{c: pb.NewPresenceServiceClient(conn)}
}

func (p *PresenceClient) Online(ctx context.Context, userID string) error {
	_, err := p.c.Online(ctx, &pb.UserRequest{UserId: userID})
	return fromStatus(err)
}
func (p *PresenceClient) Heartbeat(ctx context.Context, userID string) error {
	_, err := p.c.Heartbeat(ctx, &pb.UserRequest{UserId: userID})
	return fromStatus(err)
}
func (p *PresenceClient) Offline(ctx context.Context, userID string) error {
	_, err := p.c.Offline(ctx, &pb.UserRequest{UserId: userID})
	return fromStatus(err)
}
func (p *PresenceClient) Typing(ctx context.Context, chatID, userID string, active bool) error {
	_, err := p.c.Typing(ctx, &pb.TypingRequest{ChatId: chatID, UserId: userID, Active: active})
	return fromStatus(err)
}

// ---- Key directory ----

// NewKeyDirClient builds a key-directory gRPC client over conn.
func NewKeyDirClient(conn grpc.ClientConnInterface, log *slog.Logger) *KeyDirClient {
	return &KeyDirClient{c: pb.NewKeyDirServiceClient(conn), log: log}
}

// callCtx bounds one RPC when the caller's context has no deadline of its own,
// while still propagating its cancellation and trace context.
func callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, 3*time.Second)
}

func (k *KeyDirClient) Publish(ctx context.Context, userID, deviceID string, b wire.KeyPublishBody) keydir.State {
	ctx, cancel := callCtx(ctx)
	defer cancel()
	r, err := k.c.Publish(ctx, &pb.PublishRequest{
		UserId: userID, DeviceId: deviceID, IdentityKey: b.IdentityKey, SigningKey: b.SigningKey,
		SignedPrekey: b.SignedPreKey, SignedPrekeySig: b.SignedPreKeySig, Prekeys: b.PreKeys,
	})
	if err != nil {
		k.log.Warn("keydir publish (remote)", "err", err)
		// Empty state, not zeroed counts: the same distinction the Redis backend makes.
		// The write may well have landed; reporting zero prekeys left would tell the
		// device it holds none and provoke a republish of a batch already stored.
		return keydir.State{}
	}
	var firstSeen time.Time
	if r.SignedPrekeyFirstSeenMs > 0 {
		firstSeen = time.UnixMilli(r.SignedPrekeyFirstSeenMs)
	}
	return keydir.State{
		OneTimePreKeysLeft:    int(r.OneTimePrekeysLeft),
		Accepted:              int(r.Accepted),
		SignedPreKeyFirstSeen: firstSeen,
	}
}

func (k *KeyDirClient) Fetch(ctx context.Context, userID, deviceID string) (wire.KeyBundleBody, bool) {
	ctx, cancel := callCtx(ctx)
	defer cancel()
	r, err := k.c.Fetch(ctx, &pb.FetchRequest{UserId: userID, DeviceId: deviceID})
	if err != nil {
		k.log.Warn("keydir fetch (remote)", "err", err)
		return wire.KeyBundleBody{}, false
	}
	if !r.Found {
		return wire.KeyBundleBody{}, false
	}
	return wireKeyBundle(r.Bundle), true
}

func (k *KeyDirClient) FetchAll(ctx context.Context, userID string) []wire.KeyBundleBody {
	ctx, cancel := callCtx(ctx)
	defer cancel()
	r, err := k.c.FetchAll(ctx, &pb.UserRequest{UserId: userID})
	if err != nil {
		k.log.Warn("keydir fetchall (remote)", "err", err)
		return nil
	}
	out := make([]wire.KeyBundleBody, len(r.Bundles))
	for i, b := range r.Bundles {
		out[i] = wireKeyBundle(b)
	}
	return out
}

func (m *MessageClient) Thread(ctx context.Context, userID, chatID, rootID string, afterSeq uint64, limit int) ([]*model.Message, error) {
	r, err := m.c.Thread(ctx, &pb.ThreadRequest{UserId: userID, ChatId: chatID, RootId: rootID, AfterSeq: afterSeq, Limit: int32(limit)})
	if err != nil {
		return nil, fromStatus(err)
	}
	out := make([]*model.Message, len(r.Messages))
	for i, mm := range r.Messages {
		out[i] = modelMessage(mm)
	}
	return out, nil
}

func (m *MessageClient) Forward(ctx context.Context, userID, srcChatID, srcMsgID, dstChatID, dedupKey string) (*model.Message, bool, error) {
	r, err := m.c.Forward(ctx, &pb.ForwardRequest{
		UserId: userID, SrcChatId: srcChatID, SrcMsgId: srcMsgID, DstChatId: dstChatID, DedupKey: dedupKey,
	})
	if err != nil {
		return nil, false, fromStatus(err)
	}
	return modelMessage(r.Message), r.Duplicate, nil
}

// ---- Auth: session management ----
//
// These sit on the AuthService interface rather than behind a type assertion, so
// the split deployment cannot quietly lose them. A SessionRow carries no token
// on purpose: the list exists to let a person recognise a device, and shipping
// credentials for every session to every session would be the opposite of what
// the feature is for.

func (a *AuthClient) ListSessions(ctx context.Context, userID string) ([]*model.Session, error) {
	r, err := a.c.ListSessions(ctx, &pb.UserIDRequest{UserId: userID})
	if err != nil {
		return nil, fromStatus(err)
	}
	out := make([]*model.Session, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		out = append(out, &model.Session{
			ID: s.Id, UserID: s.UserId, DeviceID: s.DeviceId,
			CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, RevokedAt: s.RevokedAt,
		})
	}
	return out, nil
}

func (a *AuthClient) RevokeOwned(ctx context.Context, userID, sessionID string) error {
	_, err := a.c.RevokeOwned(ctx, &pb.RevokeOwnedRequest{UserId: userID, SessionId: sessionID})
	return fromStatus(err)
}

func (a *AuthClient) RevokeAll(ctx context.Context, userID, keepSessionID string) (int, error) {
	r, err := a.c.RevokeAll(ctx, &pb.RevokeAllRequest{UserId: userID, KeepSessionId: keepSessionID})
	if err != nil {
		return 0, fromStatus(err)
	}
	return int(r.Revoked), nil
}

func (a *AuthClient) DeleteAccount(ctx context.Context, userID, password string) error {
	_, err := a.c.DeleteAccount(ctx, &pb.DeleteAccountRequest{UserId: userID, Password: password})
	return fromStatus(err)
}
