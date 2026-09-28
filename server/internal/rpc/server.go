package rpc

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/auth"
	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/keydir"
	"github.com/SyncApp-chat/SyncApp/internal/message"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/presence"
	pb "github.com/SyncApp-chat/SyncApp/internal/rpc/pb"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
	"google.golang.org/grpc"
)

// ---- Auth ----

// RegisterAuth registers an auth service on a gRPC server.
func RegisterAuth(s *grpc.Server, svc *auth.Service) {
	pb.RegisterAuthServiceServer(s, &AuthServer{svc: svc})
}

func (a *AuthServer) Register(ctx context.Context, r *pb.RegisterRequest) (*pb.SessionUser, error) {
	sess, user, err := a.svc.Register(ctx, r.Username, r.Password, r.DisplayName, r.DeviceId, r.Platform)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SessionUser{Session: pbSession(sess), User: pbUser(user)}, nil
}

func (a *AuthServer) Login(ctx context.Context, r *pb.LoginRequest) (*pb.SessionUser, error) {
	sess, user, err := a.svc.Login(ctx, r.Username, r.Password, r.DeviceId, r.Platform)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SessionUser{Session: pbSession(sess), User: pbUser(user)}, nil
}

func (a *AuthServer) LoginWithCode(ctx context.Context, r *pb.LoginWithCodeRequest) (*pb.SessionUser, error) {
	sess, user, err := a.svc.LoginWithCode(ctx, r.Username, r.Password, r.Code, r.DeviceId, r.Platform)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SessionUser{Session: pbSession(sess), User: pbUser(user)}, nil
}

func (a *AuthServer) ChangePassword(ctx context.Context, r *pb.ChangePasswordRequest) (*pb.ChangePasswordReply, error) {
	n, err := a.svc.ChangePassword(ctx, r.UserId, r.OldPassword, r.NewPassword, r.KeepSessionId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ChangePasswordReply{SessionsRevoked: int32(n)}, nil
}

func (a *AuthServer) BeginTOTP(ctx context.Context, r *pb.BeginTOTPRequest) (*pb.BeginTOTPReply, error) {
	secret, uri, err := a.svc.BeginTOTP(ctx, r.UserId, r.Issuer)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.BeginTOTPReply{Secret: secret, Uri: uri}, nil
}

func (a *AuthServer) ConfirmTOTP(ctx context.Context, r *pb.ConfirmTOTPRequest) (*pb.ConfirmTOTPReply, error) {
	codes, err := a.svc.ConfirmTOTP(ctx, r.UserId, r.Code)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ConfirmTOTPReply{RecoveryCodes: codes}, nil
}

func (a *AuthServer) DisableTOTP(ctx context.Context, r *pb.DisableTOTPRequest) (*pb.Empty, error) {
	if err := a.svc.DisableTOTP(ctx, r.UserId, r.Password, r.Code); err != nil {
		return nil, toStatus(err)
	}
	return &pb.Empty{}, nil
}

func (a *AuthServer) TwoFactorState(ctx context.Context, r *pb.UserIDRequest) (*pb.TwoFactorStateReply, error) {
	return &pb.TwoFactorStateReply{
		Enabled:      a.svc.TwoFactorEnabled(ctx, r.UserId),
		RecoveryLeft: int32(a.svc.RecoveryCodesLeft(ctx, r.UserId)),
	}, nil
}

func (a *AuthServer) Authenticate(ctx context.Context, r *pb.TokenRequest) (*pb.Identity, error) {
	id, err := a.svc.Authenticate(ctx, r.Token)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.Identity{Session: pbSession(id.Session), User: pbUser(id.User)}, nil
}

func (a *AuthServer) Resume(ctx context.Context, r *pb.ResumeRequest) (*pb.Identity, error) {
	id, err := a.svc.Resume(ctx, r.ResumeToken)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.Identity{Session: pbSession(id.Session), User: pbUser(id.User)}, nil
}

// ---- Chat ----

// RegisterChat registers a chat service on a gRPC server.
func RegisterChat(s *grpc.Server, svc *chat.Service) {
	pb.RegisterChatServiceServer(s, &ChatServer{svc: svc})
}

func (c *ChatServer) EnsureDirect(ctx context.Context, r *pb.DirectRequest) (*pb.Chat, error) {
	ch, err := c.svc.EnsureDirect(ctx, r.UserA, r.UserB)
	if err != nil {
		return nil, toStatus(err)
	}
	return pbChat(ch), nil
}

func (c *ChatServer) EnsureSecret(ctx context.Context, r *pb.DirectRequest) (*pb.Chat, error) {
	ch, err := c.svc.EnsureSecret(ctx, r.UserA, r.UserB)
	if err != nil {
		return nil, toStatus(err)
	}
	return pbChat(ch), nil
}

func (c *ChatServer) FindDirect(ctx context.Context, r *pb.DirectRequest) (*pb.Chat, error) {
	ch, err := c.svc.FindDirect(ctx, r.UserA, r.UserB)
	if err != nil {
		return nil, toStatus(err)
	}
	return pbChat(ch), nil
}

func (c *ChatServer) CreateGroup(ctx context.Context, r *pb.CreateGroupRequest) (*pb.Chat, error) {
	ch, err := c.svc.CreateGroup(ctx, r.OwnerId, r.Title, model.ChatType(r.Type), r.MemberIds)
	if err != nil {
		return nil, toStatus(err)
	}
	return pbChat(ch), nil
}

func (c *ChatServer) Get(ctx context.Context, r *pb.ChatIDRequest) (*pb.Chat, error) {
	ch, err := c.svc.Get(ctx, r.ChatId)
	if err != nil {
		return nil, toStatus(err)
	}
	return pbChat(ch), nil
}

func (c *ChatServer) Members(ctx context.Context, r *pb.ChatIDRequest) (*pb.MembersReply, error) {
	ms, err := c.svc.Members(ctx, r.ChatId)
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*pb.ChatMember, len(ms))
	for i, m := range ms {
		out[i] = pbMember(m)
	}
	return &pb.MembersReply{Members: out}, nil
}

func (c *ChatServer) UserChats(ctx context.Context, r *pb.UserChatsRequest) (*pb.ChatSummariesReply, error) {
	list, err := c.svc.UserChats(ctx, r.UserId, r.After, int(r.Limit))
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*pb.ChatSummary, len(list))
	for i, s := range list {
		out[i] = pbChatSummary(s)
	}
	return &pb.ChatSummariesReply{Chats: out}, nil
}

func (c *ChatServer) UserChatPage(ctx context.Context, r *pb.UserChatPageRequest) (*pb.ChatSummariesReply, error) {
	list, err := c.svc.UserChatPage(ctx, r.UserId, chat.ChatPage{
		After: r.After, AfterActivity: r.AfterActivity,
		Limit: int(r.Limit), IncludeArchived: r.IncludeArchived,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*pb.ChatSummary, len(list))
	for i, s := range list {
		out[i] = pbChatSummary(s)
	}
	return &pb.ChatSummariesReply{Chats: out}, nil
}

func (c *ChatServer) SetChatFlags(ctx context.Context, r *pb.SetChatFlagsRequest) (*pb.ChatFlagsReply, error) {
	f, err := c.svc.SetChatFlags(ctx, r.ChatId, r.UserId, model.MemberFlags{
		MutedUntil: r.MutedUntil, Pinned: r.Pinned, Archived: r.Archived,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.ChatFlagsReply{MutedUntil: f.MutedUntil, Pinned: f.Pinned, Archived: f.Archived}, nil
}

func (c *ChatServer) UserChatIDs(ctx context.Context, r *pb.UserIDRequest) (*pb.MemberIDsReply, error) {
	ids, err := c.svc.UserChatIDs(ctx, r.UserId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.MemberIDsReply{UserIds: ids}, nil
}

func (c *ChatServer) MemberIDs(ctx context.Context, r *pb.ChatIDRequest) (*pb.MemberIDsReply, error) {
	ids, err := c.svc.MemberIDs(ctx, r.ChatId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.MemberIDsReply{UserIds: ids}, nil
}

func (c *ChatServer) MemberIDsPage(ctx context.Context, r *pb.MemberPageRequest) (*pb.MemberIDsReply, error) {
	ids, err := c.svc.MemberIDsPage(ctx, r.ChatId, r.AfterUserId, int(r.Limit))
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.MemberIDsReply{UserIds: ids}, nil
}

func (c *ChatServer) CanPost(ctx context.Context, r *pb.ChatUserRequest) (*pb.BoolReply, error) {
	ok, err := c.svc.CanPost(ctx, r.ChatId, r.UserId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.BoolReply{Ok: ok}, nil
}

func (c *ChatServer) IsMember(ctx context.Context, r *pb.ChatUserRequest) (*pb.BoolReply, error) {
	ok, err := c.svc.IsMember(ctx, r.ChatId, r.UserId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.BoolReply{Ok: ok}, nil
}

func (c *ChatServer) CanModerate(ctx context.Context, r *pb.ChatUserRequest) (*pb.BoolReply, error) {
	ok, err := c.svc.CanModerate(ctx, r.ChatId, r.UserId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.BoolReply{Ok: ok}, nil
}

// ---- Message (broker write path + read path) ----

// RegisterMessage registers the message service on a gRPC server.
func RegisterMessage(s *grpc.Server, broker *message.Broker, reader *message.Service) {
	pb.RegisterMessageServiceServer(s, &MessageServer{broker: broker, reader: reader})
}

func (m *MessageServer) Submit(ctx context.Context, r *pb.SubmitRequest) (*pb.SubmitReply, error) {
	res, err := m.broker.Submit(ctx, message.Command{
		Op:         opFromPB[r.Op],
		ActorID:    r.ActorId,
		ChatID:     r.ChatId,
		MessageID:  r.MessageId,
		DedupKey:   r.DedupKey,
		Text:       r.Text,
		MediaRef:   r.MediaRef,
		ReplyTo:    r.ReplyTo,
		Attachment: modelAttachment(r.Attachment),
		TTLSeconds: r.TtlSeconds,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SubmitReply{Message: pbMessage(res.Message), Duplicate: res.Duplicate}, nil
}

func (m *MessageServer) History(ctx context.Context, r *pb.HistoryRequest) (*pb.HistoryReply, error) {
	msgs, err := m.reader.History(ctx, r.UserId, r.ChatId, r.BeforeSeq, int(r.Limit))
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*pb.Message, len(msgs))
	for i, mm := range msgs {
		out[i] = pbMessage(mm)
	}
	return &pb.HistoryReply{Messages: out}, nil
}

func (m *MessageServer) MarkRead(ctx context.Context, r *pb.MarkReadRequest) (*pb.Empty, error) {
	if err := m.reader.MarkRead(ctx, r.UserId, r.ChatId, r.UpToSeq); err != nil {
		return nil, toStatus(err)
	}
	return &pb.Empty{}, nil
}

// ---- Presence ----

// RegisterPresence registers a presence service on a gRPC server.
func RegisterPresence(s *grpc.Server, svc *presence.Service) {
	pb.RegisterPresenceServiceServer(s, &PresenceServer{svc: svc})
}

func (p *PresenceServer) Online(ctx context.Context, r *pb.UserRequest) (*pb.Empty, error) {
	return &pb.Empty{}, p.svc.Online(ctx, r.UserId)
}
func (p *PresenceServer) Heartbeat(ctx context.Context, r *pb.UserRequest) (*pb.Empty, error) {
	return &pb.Empty{}, p.svc.Heartbeat(ctx, r.UserId)
}
func (p *PresenceServer) Offline(ctx context.Context, r *pb.UserRequest) (*pb.Empty, error) {
	return &pb.Empty{}, p.svc.Offline(ctx, r.UserId)
}
func (p *PresenceServer) Typing(ctx context.Context, r *pb.TypingRequest) (*pb.Empty, error) {
	return &pb.Empty{}, p.svc.Typing(ctx, r.ChatId, r.UserId, r.Active)
}

// ---- Key directory ----

// RegisterKeyDir registers a key directory on a gRPC server.
func RegisterKeyDir(s *grpc.Server, dir keydir.Directory) {
	pb.RegisterKeyDirServiceServer(s, &KeyDirServer{dir: dir})
}

func (k *KeyDirServer) Publish(ctx context.Context, r *pb.PublishRequest) (*pb.PublishReply, error) {
	st := k.dir.Publish(ctx, r.UserId, r.DeviceId, wire.KeyPublishBody{
		IdentityKey:     r.IdentityKey,
		SigningKey:      r.SigningKey,
		SignedPreKey:    r.SignedPrekey,
		SignedPreKeySig: r.SignedPrekeySig,
		PreKeys:         r.Prekeys,
	})
	// A zero time crosses as 0 rather than as the Unix epoch, so the caller can still
	// tell "unknown" from "published just now" - the gap this reply exists to close.
	var firstSeenMs int64
	if !st.SignedPreKeyFirstSeen.IsZero() {
		firstSeenMs = st.SignedPreKeyFirstSeen.UnixMilli()
	}
	return &pb.PublishReply{
		OneTimePrekeysLeft:      int32(st.OneTimePreKeysLeft),
		SignedPrekeyFirstSeenMs: firstSeenMs,
		Accepted:                int32(st.Accepted),
	}, nil
}

func (k *KeyDirServer) Fetch(ctx context.Context, r *pb.FetchRequest) (*pb.FetchReply, error) {
	b, ok := k.dir.Fetch(ctx, r.UserId, r.DeviceId)
	if !ok {
		return &pb.FetchReply{Found: false}, nil
	}
	return &pb.FetchReply{Bundle: pbKeyBundle(b), Found: true}, nil
}

func (k *KeyDirServer) FetchAll(ctx context.Context, r *pb.UserRequest) (*pb.FetchAllReply, error) {
	bundles := k.dir.FetchAll(ctx, r.UserId)
	out := make([]*pb.KeyBundle, len(bundles))
	for i, b := range bundles {
		out[i] = pbKeyBundle(b)
	}
	return &pb.FetchAllReply{Bundles: out}, nil
}

func (m *MessageServer) Thread(ctx context.Context, r *pb.ThreadRequest) (*pb.HistoryReply, error) {
	msgs, err := m.reader.Thread(ctx, r.UserId, r.ChatId, r.RootId, r.AfterSeq, int(r.Limit))
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*pb.Message, len(msgs))
	for i, mm := range msgs {
		out[i] = pbMessage(mm)
	}
	return &pb.HistoryReply{Messages: out}, nil
}

func (m *MessageServer) Forward(ctx context.Context, r *pb.ForwardRequest) (*pb.SubmitReply, error) {
	msg, dup, err := m.reader.Forward(ctx, r.UserId, r.SrcChatId, r.SrcMsgId, r.DstChatId, r.DedupKey)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.SubmitReply{Message: pbMessage(msg), Duplicate: dup}, nil
}

// ---- Auth: session management ----

func (a *AuthServer) ListSessions(ctx context.Context, r *pb.UserIDRequest) (*pb.SessionsReply, error) {
	sessions, err := a.svc.ListSessions(ctx, r.UserId)
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pb.SessionsReply{Sessions: make([]*pb.SessionRow, 0, len(sessions))}
	for _, s := range sessions {
		// SessionRow, not pb.Session: the latter carries Token and ResumeToken, and
		// this reply travels to a gateway that hands it to a client.
		out.Sessions = append(out.Sessions, &pb.SessionRow{
			Id: s.ID, UserId: s.UserID, DeviceId: s.DeviceID,
			CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, RevokedAt: s.RevokedAt,
		})
	}
	return out, nil
}

func (a *AuthServer) RevokeOwned(ctx context.Context, r *pb.RevokeOwnedRequest) (*pb.Empty, error) {
	if err := a.svc.RevokeOwned(ctx, r.UserId, r.SessionId); err != nil {
		return nil, toStatus(err)
	}
	return &pb.Empty{}, nil
}

func (a *AuthServer) RevokeAll(ctx context.Context, r *pb.RevokeAllRequest) (*pb.RevokeAllReply, error) {
	n, err := a.svc.RevokeAll(ctx, r.UserId, r.KeepSessionId)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.RevokeAllReply{Revoked: int32(n)}, nil
}

func (a *AuthServer) DeleteAccount(ctx context.Context, r *pb.DeleteAccountRequest) (*pb.Empty, error) {
	if err := a.svc.DeleteAccount(ctx, r.UserId, r.Password); err != nil {
		return nil, toStatus(err)
	}
	return &pb.Empty{}, nil
}
