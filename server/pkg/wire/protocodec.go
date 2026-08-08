package wire

import (
	"fmt"

	pb "github.com/synapse-chat/synapse/internal/wirepb"
	"google.golang.org/protobuf/proto"
)

func init() { bodyCodec = protoCodec{} }

// Marshal converts a wire.*Body value to protobuf bytes.
func (protoCodec) Marshal(v any) ([]byte, error) {
	m := toProto(v)
	if m == nil {
		return nil, fmt.Errorf("wire: no protobuf mapping for %T", v)
	}
	return proto.Marshal(m)
}

// Unmarshal decodes protobuf bytes into a *wire.*Body.
func (protoCodec) Unmarshal(b []byte, v any) error {
	m, apply := protoTarget(v)
	if m == nil {
		return fmt.Errorf("wire: no protobuf mapping for %T", v)
	}
	if err := proto.Unmarshal(b, m); err != nil {
		return err
	}
	apply()
	return nil
}

// toProto maps a wire body (value or pointer) to its protobuf message.
func toProto(v any) proto.Message {
	switch b := v.(type) {
	case HelloBody:
		return &pb.Hello{ClientVersion: b.ClientVersion, DeviceId: b.DeviceID, Platform: b.Platform, Caps: uint32(b.Caps), ResumeToken: b.ResumeToken}
	case WelcomeBody:
		return &pb.Welcome{ServerVersion: b.ServerVersion, SessionId: b.SessionID, Caps: uint32(b.Caps), HeartbeatMs: int32(b.HeartbeatMs), MaxInflight: int32(b.MaxInflight), ResumeSupported: b.ResumeSupported}
	case AuthBody:
		return &pb.Auth{Token: b.Token, Username: b.Username, Password: b.Password, Register: b.Register, DisplayName: b.DisplayName}
	case AuthOKBody:
		return &pb.AuthOK{
			UserId: b.UserID, DeviceId: b.DeviceID, SessionId: b.SessionID, Token: b.Token, ResumeToken: b.ResumeToken,
			Username: b.Username, DisplayName: b.DisplayName, AvatarRef: b.AvatarRef,
		}
	case SendBody:
		return &pb.Send{ChatId: b.ChatID, DedupKey: b.DedupKey, Text: b.Text, MediaRef: b.MediaRef, ReplyTo: b.ReplyTo, Attachment: attachToProto(b.Attachment), TtlSeconds: b.TTLSeconds}
	case SendAckBody:
		return &pb.SendAck{DedupKey: b.DedupKey, MessageId: b.MessageID, ChatId: b.ChatID, ChatSeq: b.ChatSeq, Timestamp: b.Timestamp, Duplicate: b.Duplicate}
	case NewMessageBody:
		return newMessageToProto(b)
	case ReadBody:
		return &pb.Read{ChatId: b.ChatID, UpToMessageId: b.UpToMessageID, UpToChatSeq: b.UpToChatSeq}
	case ReadUpdateBody:
		return &pb.ReadUpdate{ChatId: b.ChatID, UserId: b.UserID, UpToChatSeq: b.UpToChatSeq}
	case TypingBody:
		return &pb.Typing{ChatId: b.ChatID, UserId: b.UserID, Active: b.Active}
	case ReactBody:
		return &pb.React{ChatId: b.ChatID, MessageId: b.MessageID, Emoji: b.Emoji}
	case ReactUpdateBody:
		return &pb.ReactUpdate{
			ChatId: b.ChatID, MessageId: b.MessageID, UserId: b.UserID,
			Emoji: b.Emoji, Added: b.Added, Counts: countsToProto(b.Counts),
		}
	case PresenceBody:
		return &pb.Presence{UserId: b.UserID, Online: b.Online, LastSeenMs: b.LastSeenMs}
	case EditBody:
		return &pb.Edit{ChatId: b.ChatID, MessageId: b.MessageID, Text: b.Text}
	case DeleteBody:
		return &pb.Delete{ChatId: b.ChatID, MessageId: b.MessageID, ForAll: b.ForAll}
	case ThreadBody:
		return &pb.Thread{ChatId: b.ChatID, RootId: b.RootID, AfterSeq: b.AfterSeq, Limit: int32(b.Limit)}
	case ThreadOKBody:
		return &pb.ThreadOK{ChatId: b.ChatID, RootId: b.RootID, NextAfter: b.NextAfter, Done: b.Done}
	case HistoryBody:
		return &pb.History{ChatId: b.ChatID, BeforeSeq: b.BeforeSeq, Limit: int32(b.Limit)}
	case HistoryOKBody:
		return &pb.HistoryOK{ChatId: b.ChatID, NextBefore: b.NextBefore, Done: b.Done}
	case ResumeBody:
		return &pb.Resume{ResumeToken: b.ResumeToken, LastAckSeq: b.LastAckSeq}
	case ResumeOKBody:
		return &pb.ResumeOK{SessionId: b.SessionID, FromSeq: b.FromSeq}
	case ErrorBody:
		return &pb.Error{Code: uint32(b.Code), Message: b.Message, RetryAfterMs: int32(b.RetryAfterMs)}
	case MediaInitBody:
		return &pb.MediaInit{Filename: b.Filename, ContentType: b.ContentType, Size: b.Size}
	case MediaTicketBody:
		return &pb.MediaTicket{MediaRef: b.MediaRef, UploadUrl: b.UploadURL, ExpiresAt: b.ExpiresAt}
	case MediaFetchBody:
		return &pb.MediaFetch{MediaRef: b.MediaRef}
	case MediaURLBody:
		return &pb.MediaURL{MediaRef: b.MediaRef, DownloadUrl: b.DownloadURL, ExpiresAt: b.ExpiresAt}
	case SearchBody:
		return &pb.Search{Query: b.Query, Limit: int32(b.Limit)}
	case SearchResultsBody:
		return searchResultsToProto(b)
	case KeyPublishBody:
		return &pb.KeyPublish{IdentityKey: b.IdentityKey, SigningKey: b.SigningKey, SignedPrekey: b.SignedPreKey, SignedPrekeySig: b.SignedPreKeySig, Prekeys: b.PreKeys}
	case KeyFetchBody:
		return &pb.KeyFetch{UserId: b.UserID, DeviceId: b.DeviceID}
	case KeyBundleBody:
		return keyBundleToProto(b)
	case KeyBundlesBody:
		return keyBundlesToProto(b)
	case SecretMsgBody:
		return &pb.SecretMsg{ToUserId: b.ToUserID, ToDeviceId: b.ToDeviceID, FromUserId: b.FromUserID, FromDeviceId: b.FromDeviceID, RatchetHeader: b.RatchetHeader, Ciphertext: b.Ciphertext}
	case SetUsernameBody:
		return &pb.SetUsername{ChatId: b.ChatID, Username: b.Username}
	case InviteCreateBody:
		return &pb.InviteCreate{ChatId: b.ChatID, ExpiresAt: b.ExpiresAt, MaxUses: b.MaxUses}
	case InviteRevokeBody:
		return &pb.InviteRevoke{ChatId: b.ChatID, Code: b.Code}
	case InviteListBody:
		return &pb.InviteList{ChatId: b.ChatID}
	case JoinBody:
		return &pb.Join{Code: b.Code, Handle: b.Handle}
	case SetRoleBody:
		return &pb.SetRole{ChatId: b.ChatID, UserId: b.UserID, Role: b.Role}
	case PushTokenBody:
		return &pb.PushToken{Token: b.Token}
	case ChatCreateBody:
		return &pb.ChatCreate{Type: b.Type, Title: b.Title, Members: b.Members}
	case ChatInfoBody:
		return &pb.ChatInfo{ChatId: b.ChatID, Type: b.Type, Title: b.Title, OwnerId: b.OwnerID}
	case ChatListBody:
		return &pb.ChatList{After: b.After, Limit: int32(b.Limit)}
	case ChatsBody:
		return chatsToProto(b)
	case ProfileGetBody:
		return &pb.ProfileGet{Target: b.Target}
	case ProfileSetBody:
		return &pb.ProfileSet{DisplayName: b.DisplayName, AvatarRef: b.AvatarRef, ClearAvatar: b.ClearAvatar}
	case ProfileBody:
		return &pb.Profile{UserId: b.UserID, Username: b.Username, DisplayName: b.DisplayName, AvatarRef: b.AvatarRef}
	case FanoutShardBody:
		return &pb.FanoutShard{Body: newMessageToProto(b.Body), Members: b.Members}
	case InvitesBody:
		return invitesToProto(b)
	case PinBody:
		return &pb.PinAction{ChatId: b.ChatID, MessageId: b.MessageID}
	case PinnedBody:
		return pinnedToProto(b)
	case DraftBody:
		return &pb.Draft{ChatId: b.ChatID, Text: b.Text, ReplyTo: b.ReplyTo}
	case DraftSyncBody:
		return &pb.DraftSync{Since: b.Since}
	case DraftsBody:
		return draftsToProto(b)
	case ForwardBody:
		return &pb.Forward{FromChatId: b.FromChatID, MessageId: b.MessageID, ToChatId: b.ToChatID, DedupKey: b.DedupKey}
	case ScheduleBody:
		return &pb.Schedule{ChatId: b.ChatID, Text: b.Text, MediaRef: b.MediaRef, Attachment: attachToProto(b.Attachment), ReplyTo: b.ReplyTo, TtlSeconds: b.TTLSeconds, SendAt: b.SendAt}
	case ScheduleListBody:
		return &pb.ScheduleList{ChatId: b.ChatID}
	case ScheduleCancelBody:
		return &pb.ScheduleCancel{Id: b.ID}
	case ScheduledBody:
		return scheduledToProto(b)
	case ContactAddBody:
		return &pb.ContactAdd{Target: b.Target, Name: b.Name}
	case ContactRemoveBody:
		return &pb.ContactRemove{Target: b.Target}
	case ContactSyncBody:
		return &pb.ContactSync{Since: b.Since}
	case ContactListBody:
		return contactListToProto(b)
	case BlockBody:
		return &pb.Block{Target: b.Target, Blocked: b.Blocked}
	case PollCreateBody:
		return &pb.PollCreate{ChatId: b.ChatID, Question: b.Question, Options: b.Options, MultiChoice: b.MultiChoice, Anonymous: b.Anonymous}
	case PollVoteBody:
		return &pb.PollVote{PollId: b.PollID, Option: b.Option}
	case PollCloseBody:
		return &pb.PollClose{PollId: b.PollID}
	case PollStateBody:
		return pollStateToProto(b)
	case CallInviteBody:
		return &pb.CallInvite{ChatId: b.ChatID, Kind: b.Kind}
	case CallActionBody:
		return &pb.CallAction{CallId: b.CallID}
	case CallStateBody:
		return callStateToProto(b)
	case CallSignalBody:
		return &pb.CallSignal{CallId: b.CallID, ToUserId: b.ToUserID, ToDeviceId: b.ToDeviceID, FromUserId: b.FromUserID, FromDeviceId: b.FromDeviceID, SignalType: b.SignalType, Payload: b.Payload}
	case ChatExportBody:
		return &pb.ChatExport{ChatId: b.ChatID}
	case ChatExportResultBody:
		return chatExportToProto(b)
	}
	return nil
}

// protoTarget returns an empty protobuf message for a *wire.*Body target plus a
// closure that copies the decoded fields back into the target after Unmarshal.
func protoTarget(v any) (proto.Message, func()) {
	switch t := v.(type) {
	case *HelloBody:
		m := &pb.Hello{}
		return m, func() {
			*t = HelloBody{ClientVersion: m.ClientVersion, DeviceID: m.DeviceId, Platform: m.Platform, Caps: Cap(m.Caps), ResumeToken: m.ResumeToken}
		}
	case *WelcomeBody:
		m := &pb.Welcome{}
		return m, func() {
			*t = WelcomeBody{ServerVersion: m.ServerVersion, SessionID: m.SessionId, Caps: Cap(m.Caps), HeartbeatMs: int(m.HeartbeatMs), MaxInflight: int(m.MaxInflight), ResumeSupported: m.ResumeSupported}
		}
	case *AuthBody:
		m := &pb.Auth{}
		return m, func() {
			*t = AuthBody{Token: m.Token, Username: m.Username, Password: m.Password, Register: m.Register, DisplayName: m.DisplayName}
		}
	case *AuthOKBody:
		m := &pb.AuthOK{}
		return m, func() {
			*t = AuthOKBody{
				UserID: m.UserId, DeviceID: m.DeviceId, SessionID: m.SessionId, Token: m.Token, ResumeToken: m.ResumeToken,
				Username: m.Username, DisplayName: m.DisplayName, AvatarRef: m.AvatarRef,
			}
		}
	case *SendBody:
		m := &pb.Send{}
		return m, func() {
			*t = SendBody{ChatID: m.ChatId, DedupKey: m.DedupKey, Text: m.Text, MediaRef: m.MediaRef, ReplyTo: m.ReplyTo, Attachment: attachFromProto(m.Attachment), TTLSeconds: m.TtlSeconds}
		}
	case *SendAckBody:
		m := &pb.SendAck{}
		return m, func() {
			*t = SendAckBody{DedupKey: m.DedupKey, MessageID: m.MessageId, ChatID: m.ChatId, ChatSeq: m.ChatSeq, Timestamp: m.Timestamp, Duplicate: m.Duplicate}
		}
	case *NewMessageBody:
		m := &pb.NewMessage{}
		return m, func() { *t = newMessageFromProto(m) }
	case *ReadBody:
		m := &pb.Read{}
		return m, func() { *t = ReadBody{ChatID: m.ChatId, UpToMessageID: m.UpToMessageId, UpToChatSeq: m.UpToChatSeq} }
	case *ReadUpdateBody:
		m := &pb.ReadUpdate{}
		return m, func() { *t = ReadUpdateBody{ChatID: m.ChatId, UserID: m.UserId, UpToChatSeq: m.UpToChatSeq} }
	case *TypingBody:
		m := &pb.Typing{}
		return m, func() { *t = TypingBody{ChatID: m.ChatId, UserID: m.UserId, Active: m.Active} }
	case *ReactBody:
		m := &pb.React{}
		return m, func() { *t = ReactBody{ChatID: m.ChatId, MessageID: m.MessageId, Emoji: m.Emoji} }
	case *ReactUpdateBody:
		m := &pb.ReactUpdate{}
		return m, func() {
			*t = ReactUpdateBody{
				ChatID: m.ChatId, MessageID: m.MessageId, UserID: m.UserId,
				Emoji: m.Emoji, Added: m.Added, Counts: countsFromProto(m.Counts),
			}
		}
	case *PresenceBody:
		m := &pb.Presence{}
		return m, func() { *t = PresenceBody{UserID: m.UserId, Online: m.Online, LastSeenMs: m.LastSeenMs} }
	case *EditBody:
		m := &pb.Edit{}
		return m, func() { *t = EditBody{ChatID: m.ChatId, MessageID: m.MessageId, Text: m.Text} }
	case *DeleteBody:
		m := &pb.Delete{}
		return m, func() { *t = DeleteBody{ChatID: m.ChatId, MessageID: m.MessageId, ForAll: m.ForAll} }
	case *ThreadBody:
		m := &pb.Thread{}
		return m, func() { *t = ThreadBody{ChatID: m.ChatId, RootID: m.RootId, AfterSeq: m.AfterSeq, Limit: int(m.Limit)} }
	case *ThreadOKBody:
		m := &pb.ThreadOK{}
		return m, func() { *t = ThreadOKBody{ChatID: m.ChatId, RootID: m.RootId, NextAfter: m.NextAfter, Done: m.Done} }
	case *HistoryBody:
		m := &pb.History{}
		return m, func() { *t = HistoryBody{ChatID: m.ChatId, BeforeSeq: m.BeforeSeq, Limit: int(m.Limit)} }
	case *HistoryOKBody:
		m := &pb.HistoryOK{}
		return m, func() { *t = HistoryOKBody{ChatID: m.ChatId, NextBefore: m.NextBefore, Done: m.Done} }
	case *ResumeBody:
		m := &pb.Resume{}
		return m, func() { *t = ResumeBody{ResumeToken: m.ResumeToken, LastAckSeq: m.LastAckSeq} }
	case *ResumeOKBody:
		m := &pb.ResumeOK{}
		return m, func() { *t = ResumeOKBody{SessionID: m.SessionId, FromSeq: m.FromSeq} }
	case *ErrorBody:
		m := &pb.Error{}
		return m, func() { *t = ErrorBody{Code: ErrorCode(m.Code), Message: m.Message, RetryAfterMs: int(m.RetryAfterMs)} }
	case *MediaInitBody:
		m := &pb.MediaInit{}
		return m, func() { *t = MediaInitBody{Filename: m.Filename, ContentType: m.ContentType, Size: m.Size} }
	case *MediaTicketBody:
		m := &pb.MediaTicket{}
		return m, func() { *t = MediaTicketBody{MediaRef: m.MediaRef, UploadURL: m.UploadUrl, ExpiresAt: m.ExpiresAt} }
	case *MediaFetchBody:
		m := &pb.MediaFetch{}
		return m, func() { *t = MediaFetchBody{MediaRef: m.MediaRef} }
	case *MediaURLBody:
		m := &pb.MediaURL{}
		return m, func() { *t = MediaURLBody{MediaRef: m.MediaRef, DownloadURL: m.DownloadUrl, ExpiresAt: m.ExpiresAt} }
	case *SearchBody:
		m := &pb.Search{}
		return m, func() { *t = SearchBody{Query: m.Query, Limit: int(m.Limit)} }
	case *SearchResultsBody:
		m := &pb.SearchResults{}
		return m, func() { *t = searchResultsFromProto(m) }
	case *KeyPublishBody:
		m := &pb.KeyPublish{}
		return m, func() {
			*t = KeyPublishBody{IdentityKey: m.IdentityKey, SigningKey: m.SigningKey, SignedPreKey: m.SignedPrekey, SignedPreKeySig: m.SignedPrekeySig, PreKeys: m.Prekeys}
		}
	case *KeyFetchBody:
		m := &pb.KeyFetch{}
		return m, func() { *t = KeyFetchBody{UserID: m.UserId, DeviceID: m.DeviceId} }
	case *KeyBundleBody:
		m := &pb.KeyBundle{}
		return m, func() { *t = keyBundleFromProto(m) }
	case *KeyBundlesBody:
		m := &pb.KeyBundles{}
		return m, func() { *t = keyBundlesFromProto(m) }
	case *SecretMsgBody:
		m := &pb.SecretMsg{}
		return m, func() {
			*t = SecretMsgBody{ToUserID: m.ToUserId, ToDeviceID: m.ToDeviceId, FromUserID: m.FromUserId, FromDeviceID: m.FromDeviceId, RatchetHeader: m.RatchetHeader, Ciphertext: m.Ciphertext}
		}
	case *SetUsernameBody:
		m := &pb.SetUsername{}
		return m, func() { *t = SetUsernameBody{ChatID: m.ChatId, Username: m.Username} }
	case *InviteCreateBody:
		m := &pb.InviteCreate{}
		return m, func() { *t = InviteCreateBody{ChatID: m.ChatId, ExpiresAt: m.ExpiresAt, MaxUses: m.MaxUses} }
	case *InviteRevokeBody:
		m := &pb.InviteRevoke{}
		return m, func() { *t = InviteRevokeBody{ChatID: m.ChatId, Code: m.Code} }
	case *InviteListBody:
		m := &pb.InviteList{}
		return m, func() { *t = InviteListBody{ChatID: m.ChatId} }
	case *JoinBody:
		m := &pb.Join{}
		return m, func() { *t = JoinBody{Code: m.Code, Handle: m.Handle} }
	case *SetRoleBody:
		m := &pb.SetRole{}
		return m, func() { *t = SetRoleBody{ChatID: m.ChatId, UserID: m.UserId, Role: m.Role} }
	case *PushTokenBody:
		m := &pb.PushToken{}
		return m, func() { *t = PushTokenBody{Token: m.Token} }
	case *ChatCreateBody:
		m := &pb.ChatCreate{}
		return m, func() { *t = ChatCreateBody{Type: m.Type, Title: m.Title, Members: m.Members} }
	case *ChatInfoBody:
		m := &pb.ChatInfo{}
		return m, func() {
			*t = ChatInfoBody{ChatID: m.ChatId, Type: m.Type, Title: m.Title, OwnerID: m.OwnerId}
		}
	case *ChatListBody:
		m := &pb.ChatList{}
		return m, func() { *t = ChatListBody{After: m.After, Limit: int(m.Limit)} }
	case *ChatsBody:
		m := &pb.Chats{}
		return m, func() { *t = chatsFromProto(m) }
	case *ProfileGetBody:
		m := &pb.ProfileGet{}
		return m, func() { *t = ProfileGetBody{Target: m.Target} }
	case *ProfileSetBody:
		m := &pb.ProfileSet{}
		return m, func() {
			*t = ProfileSetBody{DisplayName: m.DisplayName, AvatarRef: m.AvatarRef, ClearAvatar: m.ClearAvatar}
		}
	case *ProfileBody:
		m := &pb.Profile{}
		return m, func() {
			*t = ProfileBody{UserID: m.UserId, Username: m.Username, DisplayName: m.DisplayName, AvatarRef: m.AvatarRef}
		}
	case *FanoutShardBody:
		m := &pb.FanoutShard{}
		return m, func() {
			*t = FanoutShardBody{Members: m.Members}
			if m.Body != nil {
				t.Body = newMessageFromProto(m.Body)
			}
		}
	case *InvitesBody:
		m := &pb.Invites{}
		return m, func() { *t = invitesFromProto(m) }
	case *PinBody:
		m := &pb.PinAction{}
		return m, func() { *t = PinBody{ChatID: m.ChatId, MessageID: m.MessageId} }
	case *PinnedBody:
		m := &pb.Pinned{}
		return m, func() { *t = pinnedFromProto(m) }
	case *DraftBody:
		m := &pb.Draft{}
		return m, func() { *t = DraftBody{ChatID: m.ChatId, Text: m.Text, ReplyTo: m.ReplyTo} }
	case *DraftSyncBody:
		m := &pb.DraftSync{}
		return m, func() { *t = DraftSyncBody{Since: m.Since} }
	case *DraftsBody:
		m := &pb.Drafts{}
		return m, func() { *t = draftsFromProto(m) }
	case *ForwardBody:
		m := &pb.Forward{}
		return m, func() {
			*t = ForwardBody{FromChatID: m.FromChatId, MessageID: m.MessageId, ToChatID: m.ToChatId, DedupKey: m.DedupKey}
		}
	case *ScheduleBody:
		m := &pb.Schedule{}
		return m, func() {
			*t = ScheduleBody{ChatID: m.ChatId, Text: m.Text, MediaRef: m.MediaRef, Attachment: attachFromProto(m.Attachment), ReplyTo: m.ReplyTo, TTLSeconds: m.TtlSeconds, SendAt: m.SendAt}
		}
	case *ScheduleListBody:
		m := &pb.ScheduleList{}
		return m, func() { *t = ScheduleListBody{ChatID: m.ChatId} }
	case *ScheduleCancelBody:
		m := &pb.ScheduleCancel{}
		return m, func() { *t = ScheduleCancelBody{ID: m.Id} }
	case *ScheduledBody:
		m := &pb.Scheduled{}
		return m, func() { *t = scheduledFromProto(m) }
	case *ContactAddBody:
		m := &pb.ContactAdd{}
		return m, func() { *t = ContactAddBody{Target: m.Target, Name: m.Name} }
	case *ContactRemoveBody:
		m := &pb.ContactRemove{}
		return m, func() { *t = ContactRemoveBody{Target: m.Target} }
	case *ContactSyncBody:
		m := &pb.ContactSync{}
		return m, func() { *t = ContactSyncBody{Since: m.Since} }
	case *ContactListBody:
		m := &pb.ContactList{}
		return m, func() { *t = contactListFromProto(m) }
	case *BlockBody:
		m := &pb.Block{}
		return m, func() { *t = BlockBody{Target: m.Target, Blocked: m.Blocked} }
	case *PollCreateBody:
		m := &pb.PollCreate{}
		return m, func() {
			*t = PollCreateBody{ChatID: m.ChatId, Question: m.Question, Options: m.Options, MultiChoice: m.MultiChoice, Anonymous: m.Anonymous}
		}
	case *PollVoteBody:
		m := &pb.PollVote{}
		return m, func() { *t = PollVoteBody{PollID: m.PollId, Option: m.Option} }
	case *PollCloseBody:
		m := &pb.PollClose{}
		return m, func() { *t = PollCloseBody{PollID: m.PollId} }
	case *PollStateBody:
		m := &pb.PollState{}
		return m, func() { *t = pollStateFromProto(m) }
	case *CallInviteBody:
		m := &pb.CallInvite{}
		return m, func() { *t = CallInviteBody{ChatID: m.ChatId, Kind: m.Kind} }
	case *CallActionBody:
		m := &pb.CallAction{}
		return m, func() { *t = CallActionBody{CallID: m.CallId} }
	case *CallStateBody:
		m := &pb.CallState{}
		return m, func() { *t = callStateFromProto(m) }
	case *CallSignalBody:
		m := &pb.CallSignal{}
		return m, func() {
			*t = CallSignalBody{CallID: m.CallId, ToUserID: m.ToUserId, ToDeviceID: m.ToDeviceId, FromUserID: m.FromUserId, FromDeviceID: m.FromDeviceId, SignalType: m.SignalType, Payload: m.Payload}
		}
	case *ChatExportBody:
		m := &pb.ChatExport{}
		return m, func() { *t = ChatExportBody{ChatID: m.ChatId} }
	case *ChatExportResultBody:
		m := &pb.ChatExportResult{}
		return m, func() { *t = chatExportFromProto(m) }
	}
	return nil, nil
}

// --- helpers for the composite/repeated bodies ---

func newMessageToProto(b NewMessageBody) *pb.NewMessage {
	return &pb.NewMessage{
		MessageId: b.MessageID, ChatId: b.ChatID, SenderId: b.SenderID, ChatSeq: b.ChatSeq,
		Text: b.Text, MediaRef: b.MediaRef, ReplyTo: b.ReplyTo, Edited: b.Edited, Deleted: b.Deleted, Timestamp: b.Timestamp,
		Attachment: attachToProto(b.Attachment), ThreadRoot: b.ThreadRoot, ReplyCount: b.ReplyCount,
		Forward: fwdToProto(b.Forward), ExpiresAt: b.ExpiresAt,
	}
}

func newMessageFromProto(m *pb.NewMessage) NewMessageBody {
	return NewMessageBody{
		MessageID: m.MessageId, ChatID: m.ChatId, SenderID: m.SenderId, ChatSeq: m.ChatSeq,
		Text: m.Text, MediaRef: m.MediaRef, ReplyTo: m.ReplyTo, Edited: m.Edited, Deleted: m.Deleted, Timestamp: m.Timestamp,
		Attachment: attachFromProto(m.Attachment), ThreadRoot: m.ThreadRoot, ReplyCount: m.ReplyCount,
		Forward: fwdFromProto(m.Forward), ExpiresAt: m.ExpiresAt,
	}
}

func searchResultsToProto(b SearchResultsBody) *pb.SearchResults {
	out := &pb.SearchResults{Query: b.Query}
	for _, h := range b.Hits {
		out.Hits = append(out.Hits, &pb.SearchHit{MessageId: h.MessageID, ChatId: h.ChatID, SenderId: h.SenderID, Seq: h.Seq, Text: h.Text})
	}
	return out
}

func searchResultsFromProto(m *pb.SearchResults) SearchResultsBody {
	out := SearchResultsBody{Query: m.Query}
	for _, h := range m.Hits {
		out.Hits = append(out.Hits, SearchHit{MessageID: h.MessageId, ChatID: h.ChatId, SenderID: h.SenderId, Seq: h.Seq, Text: h.Text})
	}
	return out
}

func keyBundleToProto(b KeyBundleBody) *pb.KeyBundle {
	return &pb.KeyBundle{
		UserId: b.UserID, DeviceId: b.DeviceID, IdentityKey: b.IdentityKey, SigningKey: b.SigningKey,
		SignedPrekey: b.SignedPreKey, SignedPrekeySig: b.SignedPreKeySig, OneTimePrekey: b.OneTimePreKey,
	}
}

func keyBundleFromProto(m *pb.KeyBundle) KeyBundleBody {
	return KeyBundleBody{
		UserID: m.UserId, DeviceID: m.DeviceId, IdentityKey: m.IdentityKey, SigningKey: m.SigningKey,
		SignedPreKey: m.SignedPrekey, SignedPreKeySig: m.SignedPrekeySig, OneTimePreKey: m.OneTimePrekey,
	}
}

func keyBundlesToProto(b KeyBundlesBody) *pb.KeyBundles {
	out := &pb.KeyBundles{UserId: b.UserID}
	for _, kb := range b.Bundles {
		out.Bundles = append(out.Bundles, keyBundleToProto(kb))
	}
	return out
}

func keyBundlesFromProto(m *pb.KeyBundles) KeyBundlesBody {
	out := KeyBundlesBody{UserID: m.UserId}
	for _, kb := range m.Bundles {
		out.Bundles = append(out.Bundles, keyBundleFromProto(kb))
	}
	return out
}

func chatExportToProto(b ChatExportResultBody) *pb.ChatExportResult {
	out := &pb.ChatExportResult{ChatId: b.ChatID, Type: b.Type, Title: b.Title, OwnerId: b.OwnerID, Done: b.Done}
	for _, m := range b.Members {
		out.Members = append(out.Members, &pb.ChatMember{UserId: m.UserID, Role: m.Role, JoinedAt: m.JoinedAt})
	}
	for _, msg := range b.Messages {
		out.Messages = append(out.Messages, newMessageToProto(msg))
	}
	return out
}

func chatExportFromProto(m *pb.ChatExportResult) ChatExportResultBody {
	out := ChatExportResultBody{ChatID: m.ChatId, Type: m.Type, Title: m.Title, OwnerID: m.OwnerId, Done: m.Done}
	for _, mm := range m.Members {
		out.Members = append(out.Members, ChatMemberInfo{UserID: mm.UserId, Role: mm.Role, JoinedAt: mm.JoinedAt})
	}
	for _, msg := range m.Messages {
		out.Messages = append(out.Messages, newMessageFromProto(msg))
	}
	return out
}

// countsToProto/countsFromProto bridge the reaction tally between the Go body
// (map[string]int) and protobuf (map[string]int32).
func countsToProto(m map[string]int) map[string]int32 {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int32, len(m))
	for k, v := range m {
		out[k] = int32(v)
	}
	return out
}

func countsFromProto(m map[string]int32) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = int(v)
	}
	return out
}

// attachToProto/attachFromProto bridge the attachment descriptor.
func attachToProto(a *Attachment) *pb.Attachment {
	if a == nil {
		return nil
	}
	return &pb.Attachment{
		Kind: a.Kind, MediaRef: a.MediaRef, Filename: a.Filename, Mime: a.MIME,
		Size: a.Size, DurationMs: a.DurationMs, Waveform: a.Waveform,
		Width: a.Width, Height: a.Height, ThumbRef: a.ThumbRef,
	}
}

func attachFromProto(a *pb.Attachment) *Attachment {
	if a == nil {
		return nil
	}
	return &Attachment{
		Kind: a.Kind, MediaRef: a.MediaRef, Filename: a.Filename, MIME: a.Mime,
		Size: a.Size, DurationMs: a.DurationMs, Waveform: a.Waveform,
		Width: a.Width, Height: a.Height, ThumbRef: a.ThumbRef,
	}
}

func callStateToProto(b CallStateBody) *pb.CallState {
	out := &pb.CallState{
		CallId: b.CallID, ChatId: b.ChatID, InitiatorId: b.InitiatorID,
		Kind: b.Kind, State: b.State,
	}
	for _, p := range b.Participants {
		out.Participants = append(out.Participants, &pb.CallParticipant{
			UserId: p.UserID, DeviceId: p.DeviceID, State: p.State,
		})
	}
	return out
}

func callStateFromProto(m *pb.CallState) CallStateBody {
	b := CallStateBody{
		CallID: m.CallId, ChatID: m.ChatId, InitiatorID: m.InitiatorId,
		Kind: m.Kind, State: m.State,
	}
	for _, p := range m.Participants {
		b.Participants = append(b.Participants, CallParticipant{
			UserID: p.UserId, DeviceID: p.DeviceId, State: p.State,
		})
	}
	return b
}

func pollStateToProto(b PollStateBody) *pb.PollState {
	out := &pb.PollState{
		PollId: b.PollID, ChatId: b.ChatID, MessageId: b.MessageID, Question: b.Question,
		TotalVotes: b.TotalVotes, MultiChoice: b.MultiChoice, Anonymous: b.Anonymous,
		Closed: b.Closed, MyVotes: b.MyVotes,
	}
	for _, o := range b.Options {
		out.Options = append(out.Options, &pb.PollOption{Index: o.Index, Text: o.Text, Votes: o.Votes})
	}
	return out
}

func pollStateFromProto(m *pb.PollState) PollStateBody {
	b := PollStateBody{
		PollID: m.PollId, ChatID: m.ChatId, MessageID: m.MessageId, Question: m.Question,
		TotalVotes: m.TotalVotes, MultiChoice: m.MultiChoice, Anonymous: m.Anonymous,
		Closed: m.Closed, MyVotes: m.MyVotes,
	}
	for _, o := range m.Options {
		b.Options = append(b.Options, PollOption{Index: o.Index, Text: o.Text, Votes: o.Votes})
	}
	return b
}

func contactListToProto(b ContactListBody) *pb.ContactList {
	out := &pb.ContactList{Cursor: b.Cursor}
	for _, c := range b.Contacts {
		out.Contacts = append(out.Contacts, &pb.Contact{
			UserId: c.UserID, Name: c.Name, Blocked: c.Blocked, UpdatedAt: c.UpdatedAt,
		})
	}
	return out
}

func contactListFromProto(m *pb.ContactList) ContactListBody {
	b := ContactListBody{Cursor: m.Cursor}
	for _, c := range m.Contacts {
		b.Contacts = append(b.Contacts, Contact{
			UserID: c.UserId, Name: c.Name, Blocked: c.Blocked, UpdatedAt: c.UpdatedAt,
		})
	}
	return b
}

func fwdToProto(f *ForwardOrigin) *pb.ForwardOrigin {
	if f == nil {
		return nil
	}
	return &pb.ForwardOrigin{ChatId: f.ChatID, MessageId: f.MessageID, SenderId: f.SenderID}
}

func fwdFromProto(f *pb.ForwardOrigin) *ForwardOrigin {
	if f == nil {
		return nil
	}
	return &ForwardOrigin{ChatID: f.ChatId, MessageID: f.MessageId, SenderID: f.SenderId}
}

func scheduledToProto(b ScheduledBody) *pb.Scheduled {
	out := &pb.Scheduled{}
	for _, i := range b.Items {
		out.Items = append(out.Items, &pb.ScheduledItem{Id: i.ID, ChatId: i.ChatID, Text: i.Text, SendAt: i.SendAt})
	}
	return out
}

func scheduledFromProto(m *pb.Scheduled) ScheduledBody {
	var b ScheduledBody
	for _, i := range m.Items {
		b.Items = append(b.Items, ScheduledItem{ID: i.Id, ChatID: i.ChatId, Text: i.Text, SendAt: i.SendAt})
	}
	return b
}

func pinnedToProto(b PinnedBody) *pb.Pinned {
	out := &pb.Pinned{ChatId: b.ChatID}
	for _, p := range b.Pins {
		out.Pins = append(out.Pins, &pb.Pin{MessageId: p.MessageID, PinnedBy: p.PinnedBy, PinnedAt: p.PinnedAt})
	}
	return out
}

func pinnedFromProto(m *pb.Pinned) PinnedBody {
	b := PinnedBody{ChatID: m.ChatId}
	for _, p := range m.Pins {
		b.Pins = append(b.Pins, Pin{MessageID: p.MessageId, PinnedBy: p.PinnedBy, PinnedAt: p.PinnedAt})
	}
	return b
}

func draftsToProto(b DraftsBody) *pb.Drafts {
	out := &pb.Drafts{Cursor: b.Cursor}
	for _, d := range b.Drafts {
		out.Drafts = append(out.Drafts, &pb.DraftItem{ChatId: d.ChatID, Text: d.Text, ReplyTo: d.ReplyTo, UpdatedAt: d.UpdatedAt})
	}
	return out
}

func draftsFromProto(m *pb.Drafts) DraftsBody {
	b := DraftsBody{Cursor: m.Cursor}
	for _, d := range m.Drafts {
		b.Drafts = append(b.Drafts, DraftItem{ChatID: d.ChatId, Text: d.Text, ReplyTo: d.ReplyTo, UpdatedAt: d.UpdatedAt})
	}
	return b
}

func chatsToProto(b ChatsBody) *pb.Chats {
	out := &pb.Chats{NextAfter: b.NextAfter, Done: b.Done}
	for _, c := range b.Chats {
		out.Chats = append(out.Chats, &pb.ChatSummary{
			ChatId: c.ChatID, Type: c.Type, Title: c.Title, OwnerId: c.OwnerID,
			Username: c.Username, LastSeq: c.LastSeq, MyRole: c.MyRole, PeerId: c.PeerID,
		})
	}
	return out
}

func chatsFromProto(m *pb.Chats) ChatsBody {
	b := ChatsBody{NextAfter: m.NextAfter, Done: m.Done}
	for _, c := range m.Chats {
		b.Chats = append(b.Chats, ChatSummary{
			ChatID: c.ChatId, Type: c.Type, Title: c.Title, OwnerID: c.OwnerId,
			Username: c.Username, LastSeq: c.LastSeq, MyRole: c.MyRole, PeerID: c.PeerId,
		})
	}
	return b
}

func invitesToProto(b InvitesBody) *pb.Invites {
	out := &pb.Invites{JoinedChat: b.JoinedChat}
	for _, l := range b.Links {
		out.Links = append(out.Links, &pb.InviteLink{
			Code: l.Code, ChatId: l.ChatID, ExpiresAt: l.ExpiresAt, MaxUses: l.MaxUses, Uses: l.Uses,
		})
	}
	return out
}

func invitesFromProto(m *pb.Invites) InvitesBody {
	b := InvitesBody{JoinedChat: m.JoinedChat}
	for _, l := range m.Links {
		b.Links = append(b.Links, InviteLink{
			Code: l.Code, ChatID: l.ChatId, ExpiresAt: l.ExpiresAt, MaxUses: l.MaxUses, Uses: l.Uses,
		})
	}
	return b
}
