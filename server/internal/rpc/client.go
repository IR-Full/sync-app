package rpc

import (
	"context"
	"log/slog"
	"time"

	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/model"
	pb "github.com/synapse-chat/synapse/internal/rpc/pb"
	"github.com/synapse-chat/synapse/pkg/wire"
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

func (k *KeyDirClient) Publish(ctx context.Context, userID, deviceID string, b wire.KeyPublishBody) {
	ctx, cancel := callCtx(ctx)
	defer cancel()
	if _, err := k.c.Publish(ctx, &pb.PublishRequest{
		UserId: userID, DeviceId: deviceID, IdentityKey: b.IdentityKey, SigningKey: b.SigningKey,
		SignedPrekey: b.SignedPreKey, SignedPrekeySig: b.SignedPreKeySig, Prekeys: b.PreKeys,
	}); err != nil {
		k.log.Warn("keydir publish (remote)", "err", err)
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
