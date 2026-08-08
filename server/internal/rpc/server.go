package rpc

import (
	"context"

	"github.com/synapse-chat/synapse/internal/auth"
	"github.com/synapse-chat/synapse/internal/chat"
	"github.com/synapse-chat/synapse/internal/keydir"
	"github.com/synapse-chat/synapse/internal/message"
	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/presence"
	pb "github.com/synapse-chat/synapse/internal/rpc/pb"
	"github.com/synapse-chat/synapse/pkg/wire"
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

func (k *KeyDirServer) Publish(ctx context.Context, r *pb.PublishRequest) (*pb.Empty, error) {
	k.dir.Publish(ctx, r.UserId, r.DeviceId, wire.KeyPublishBody{
		IdentityKey:     r.IdentityKey,
		SigningKey:      r.SigningKey,
		SignedPreKey:    r.SignedPrekey,
		SignedPreKeySig: r.SignedPrekeySig,
		PreKeys:         r.Prekeys,
	})
	return &pb.Empty{}, nil
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
