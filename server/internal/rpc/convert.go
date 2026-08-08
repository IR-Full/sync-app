// Package rpc adapts the in-process domain services to gRPC: server adapters
// (server.go) expose a concrete service over gRPC, and client adapters
// (client.go) satisfy the gateway's service interfaces by calling a remote
// service. convert.go holds the domain<->protobuf mappers shared by both sides.
package rpc

import (
	"github.com/synapse-chat/synapse/internal/model"
	pb "github.com/synapse-chat/synapse/internal/rpc/pb"
	"github.com/synapse-chat/synapse/pkg/wire"
)

func pbUser(u *model.User) *pb.User {
	if u == nil {
		return nil
	}
	return &pb.User{Id: u.ID, Username: u.Username, DisplayName: u.DisplayName, AvatarRef: u.AvatarRef, CreatedAt: u.CreatedAt}
}

func modelUser(u *pb.User) *model.User {
	if u == nil {
		return nil
	}
	return &model.User{ID: u.Id, Username: u.Username, DisplayName: u.DisplayName, AvatarRef: u.AvatarRef, CreatedAt: u.CreatedAt}
}

func pbSession(s *model.Session) *pb.Session {
	if s == nil {
		return nil
	}
	return &pb.Session{
		Id: s.ID, UserId: s.UserID, DeviceId: s.DeviceID, Token: s.Token,
		ResumeToken: s.ResumeToken, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, RevokedAt: s.RevokedAt,
	}
}

func modelSession(s *pb.Session) *model.Session {
	if s == nil {
		return nil
	}
	return &model.Session{
		ID: s.Id, UserID: s.UserId, DeviceID: s.DeviceId, Token: s.Token,
		ResumeToken: s.ResumeToken, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, RevokedAt: s.RevokedAt,
	}
}

func pbChat(c *model.Chat) *pb.Chat {
	if c == nil {
		return nil
	}
	return &pb.Chat{Id: c.ID, Type: string(c.Type), Title: c.Title, OwnerId: c.OwnerID, CreatedAt: c.CreatedAt, LastSeq: c.LastSeq, Username: c.Username}
}

func modelChat(c *pb.Chat) *model.Chat {
	if c == nil {
		return nil
	}
	return &model.Chat{ID: c.Id, Type: model.ChatType(c.Type), Title: c.Title, OwnerID: c.OwnerId, CreatedAt: c.CreatedAt, LastSeq: c.LastSeq, Username: c.Username}
}

func pbChatSummary(s model.ChatSummary) *pb.ChatSummary {
	return &pb.ChatSummary{Chat: pbChat(s.Chat), MyRole: string(s.MyRole), PeerId: s.PeerID}
}

func modelChatSummary(s *pb.ChatSummary) model.ChatSummary {
	return model.ChatSummary{Chat: modelChat(s.Chat), MyRole: model.MemberRole(s.MyRole), PeerID: s.PeerId}
}

func pbMember(m *model.ChatMember) *pb.ChatMember {
	if m == nil {
		return nil
	}
	return &pb.ChatMember{ChatId: m.ChatID, UserId: m.UserID, Role: string(m.Role), JoinedAt: m.JoinedAt, Muted: m.Muted}
}

func modelMember(m *pb.ChatMember) *model.ChatMember {
	if m == nil {
		return nil
	}
	return &model.ChatMember{ChatID: m.ChatId, UserID: m.UserId, Role: model.MemberRole(m.Role), JoinedAt: m.JoinedAt, Muted: m.Muted}
}

// pbMessage / modelMessage carry the WHOLE message. A field dropped here is
// invisible on the split deployment only — the monolith keeps showing it — so an
// omission looks like a bug in the transport rather than in the mapping.
func pbMessage(m *model.Message) *pb.Message {
	if m == nil {
		return nil
	}
	return &pb.Message{
		Id: m.ID, ChatId: m.ChatID, SenderId: m.SenderID, Seq: m.Seq, Text: m.Text,
		MediaRef: m.MediaRef, ReplyTo: m.ReplyTo, Edited: m.Edited, Deleted: m.Deleted,
		CreatedAt: m.CreatedAt, EditedAt: m.EditedAt,
		Attachment: pbAttachment(m.Attachment), ThreadRoot: m.ThreadRoot, ReplyCount: m.ReplyCount,
		Forward: pbForward(m.Forward), ExpiresAt: m.ExpiresAt,
	}
}

func modelMessage(m *pb.Message) *model.Message {
	if m == nil {
		return nil
	}
	return &model.Message{
		ID: m.Id, ChatID: m.ChatId, SenderID: m.SenderId, Seq: m.Seq, Text: m.Text,
		MediaRef: m.MediaRef, ReplyTo: m.ReplyTo, Edited: m.Edited, Deleted: m.Deleted,
		CreatedAt: m.CreatedAt, EditedAt: m.EditedAt,
		Attachment: modelAttachment(m.Attachment), ThreadRoot: m.ThreadRoot, ReplyCount: m.ReplyCount,
		Forward: modelForward(m.Forward), ExpiresAt: m.ExpiresAt,
	}
}

func pbAttachment(a *model.Attachment) *pb.Attachment {
	if a == nil {
		return nil
	}
	return &pb.Attachment{
		Kind: string(a.Kind), MediaRef: a.MediaRef, Filename: a.Filename, Mime: a.MIME,
		Size: a.Size, DurationMs: a.DurationMs, Waveform: a.Waveform,
		Width: a.Width, Height: a.Height, ThumbRef: a.ThumbRef,
	}
}

func modelAttachment(a *pb.Attachment) *model.Attachment {
	if a == nil {
		return nil
	}
	return &model.Attachment{
		Kind: model.AttachmentKind(a.Kind), MediaRef: a.MediaRef, Filename: a.Filename, MIME: a.Mime,
		Size: a.Size, DurationMs: a.DurationMs, Waveform: a.Waveform,
		Width: a.Width, Height: a.Height, ThumbRef: a.ThumbRef,
	}
}

func pbForward(f *model.ForwardOrigin) *pb.ForwardOrigin {
	if f == nil {
		return nil
	}
	return &pb.ForwardOrigin{ChatId: f.ChatID, MessageId: f.MessageID, SenderId: f.SenderID}
}

func modelForward(f *pb.ForwardOrigin) *model.ForwardOrigin {
	if f == nil {
		return nil
	}
	return &model.ForwardOrigin{ChatID: f.ChatId, MessageID: f.MessageId, SenderID: f.SenderId}
}

func pbKeyBundle(b wire.KeyBundleBody) *pb.KeyBundle {
	return &pb.KeyBundle{
		UserId: b.UserID, DeviceId: b.DeviceID, IdentityKey: b.IdentityKey, SigningKey: b.SigningKey,
		SignedPrekey: b.SignedPreKey, SignedPrekeySig: b.SignedPreKeySig, OneTimePrekey: b.OneTimePreKey,
	}
}

func wireKeyBundle(b *pb.KeyBundle) wire.KeyBundleBody {
	if b == nil {
		return wire.KeyBundleBody{}
	}
	return wire.KeyBundleBody{
		UserID: b.UserId, DeviceID: b.DeviceId, IdentityKey: b.IdentityKey, SigningKey: b.SigningKey,
		SignedPreKey: b.SignedPrekey, SignedPreKeySig: b.SignedPrekeySig, OneTimePreKey: b.OneTimePrekey,
	}
}
