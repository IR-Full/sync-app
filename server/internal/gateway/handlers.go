package gateway

import (
	"context"
	"strings"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/pkg/wire"
)

func (c *conn) authByToken(ctx context.Context, token string) (*authIdentity, error) {
	id, err := c.gw.svc.Auth.Authenticate(ctx, token)
	if err != nil {
		return nil, err
	}
	return &authIdentity{
		userID:      id.User.ID,
		deviceID:    id.Session.DeviceID,
		sessionID:   id.Session.ID,
		token:       token,
		resumeToken: id.Session.ResumeToken,
		username:    id.User.Username,
		displayName: id.User.DisplayName,
		avatarRef:   id.User.AvatarRef,
	}, nil
}

func (c *conn) authByPassword(ctx context.Context, username, password, displayName string, register bool) (*authIdentity, error) {
	platform := c.platform
	if platform == "" {
		platform = "unknown"
	}
	// Brute-force defense: throttle attempts per username across all connections.
	if !register && !c.gw.loginLimiter.Allow(strings.ToLower(username)) {
		return nil, errLoginThrottled
	}
	// Register and login are explicit, separate intents: we never silently
	// create an account on a failed login (which would enable account-existence
	// probing and accidental takeover of typo'd usernames).
	var (
		sess *model.Session
		user *model.User
		err  error
	)
	if register {
		// The display name given at registration is honoured (the auth service
		// falls back to the username when it is empty). It is NOT read on login:
		// after the account exists, PROFILE_SET is the only writer, so a stale
		// client cannot silently revert a name the user changed elsewhere.
		displayName = strings.TrimSpace(displayName)
		if displayName != "" && !validDisplayName(displayName) {
			return nil, errBadDisplayName
		}
		sess, user, err = c.gw.svc.Auth.Register(ctx, username, password, displayName, c.deviceID, platform)
	} else {
		sess, user, err = c.gw.svc.Auth.Login(ctx, username, password, c.deviceID, platform)
	}
	if err != nil {
		return nil, err
	}
	return &authIdentity{
		userID:      user.ID,
		deviceID:    sess.DeviceID,
		sessionID:   sess.ID,
		token:       sess.Token,
		resumeToken: sess.ResumeToken,
		username:    user.Username,
		displayName: user.DisplayName,
		avatarRef:   user.AvatarRef,
	}, nil
}

// stateChanging reports whether a message type is subject to per-connection
// flood control (the expensive/abusable operations). Liveness and read-only
// control messages are exempt.
func stateChanging(t wire.MsgType) bool {
	switch t {
	case wire.MsgSend, wire.MsgEdit, wire.MsgDelete, wire.MsgSecretSend, wire.MsgReact,
		wire.MsgCallInvite, wire.MsgCallAccept, wire.MsgCallDecline, wire.MsgCallHangup,
		wire.MsgPollCreate, wire.MsgPollVote, wire.MsgPollClose,
		wire.MsgContactAdd, wire.MsgContactRemove, wire.MsgBlock,
		wire.MsgForward, wire.MsgSchedule, wire.MsgScheduleCancel,
		wire.MsgPin, wire.MsgUnpin, wire.MsgDraftSet,
		wire.MsgSetUsername, wire.MsgInviteCreate, wire.MsgInviteRevoke, wire.MsgJoin, wire.MsgSetRole,
		wire.MsgChatCreate, wire.MsgPushToken, wire.MsgProfileSet,
		// ProfileGet is read-only but resolves handles, which makes an unmetered
		// one a username-enumeration primitive.
		wire.MsgProfileGet,
		wire.MsgMediaInit, wire.MsgMediaFetch, wire.MsgKeyFetch, wire.MsgKeyPublish,
		wire.MsgKeyFetchAll, wire.MsgChatExport, wire.MsgSearch:
		return true
	default:
		return false
	}
}

// dispatch routes an authenticated inbound envelope to the right handler.
func (c *conn) dispatch(ctx context.Context, e wire.Envelope) error {
	if stateChanging(e.Type) && !c.sendLimit.Allow() {
		return c.replyErrorRetry(e.RequestID, wire.ErrFlood, "rate limited", 1000)
	}
	switch e.Type {
	case wire.MsgPing:
		return c.reply(wire.MsgPong, e.RequestID, nil)
	case wire.MsgPong:
		return nil // liveness already refreshed in observe()
	case wire.MsgTransportAck:
		return nil // cumulative ack captured via Envelope.Ack in observe()
	case wire.MsgSend:
		return c.handleSend(ctx, e)
	case wire.MsgRead:
		return c.handleRead(ctx, e)
	case wire.MsgTyping:
		return c.handleTyping(ctx, e)
	case wire.MsgReact:
		return c.handleReact(ctx, e)
	case wire.MsgThread:
		return c.handleThread(ctx, e)
	case wire.MsgCallInvite:
		return c.handleCallInvite(ctx, e)
	case wire.MsgCallAccept:
		return c.handleCallAccept(ctx, e)
	case wire.MsgCallDecline:
		return c.handleCallDecline(ctx, e)
	case wire.MsgCallHangup:
		return c.handleCallHangup(ctx, e)
	case wire.MsgCallSignal:
		return c.handleCallSignal(ctx, e)
	case wire.MsgPollCreate:
		return c.handlePollCreate(ctx, e)
	case wire.MsgPollVote:
		return c.handlePollVote(ctx, e)
	case wire.MsgPollClose:
		return c.handlePollClose(ctx, e)
	case wire.MsgContactAdd:
		return c.handleContactAdd(ctx, e)
	case wire.MsgContactRemove:
		return c.handleContactRemove(ctx, e)
	case wire.MsgContactSync:
		return c.handleContactSync(ctx, e)
	case wire.MsgBlock:
		return c.handleBlock(ctx, e)
	case wire.MsgForward:
		return c.handleForward(ctx, e)
	case wire.MsgSchedule:
		return c.handleSchedule(ctx, e)
	case wire.MsgScheduleList:
		return c.handleScheduleList(ctx, e)
	case wire.MsgScheduleCancel:
		return c.handleScheduleCancel(ctx, e)
	case wire.MsgPin:
		return c.handlePin(ctx, e, false)
	case wire.MsgUnpin:
		return c.handlePin(ctx, e, true)
	case wire.MsgPinList:
		return c.handlePinList(ctx, e)
	case wire.MsgDraftSet:
		return c.handleDraftSet(ctx, e)
	case wire.MsgDraftSync:
		return c.handleDraftSync(ctx, e)
	case wire.MsgSetUsername:
		return c.handleSetUsername(ctx, e)
	case wire.MsgInviteCreate:
		return c.handleInviteCreate(ctx, e)
	case wire.MsgInviteRevoke:
		return c.handleInviteRevoke(ctx, e)
	case wire.MsgInviteList:
		return c.handleInviteList(ctx, e)
	case wire.MsgJoin:
		return c.handleJoin(ctx, e)
	case wire.MsgSetRole:
		return c.handleSetRole(ctx, e)
	case wire.MsgChatCreate:
		return c.handleChatCreate(ctx, e)
	case wire.MsgChatList:
		return c.handleChatList(ctx, e)
	case wire.MsgProfileGet:
		return c.handleProfileGet(ctx, e)
	case wire.MsgProfileSet:
		return c.handleProfileSet(ctx, e)
	case wire.MsgPushToken:
		return c.handlePushToken(ctx, e)
	case wire.MsgEdit:
		return c.handleEdit(ctx, e)
	case wire.MsgDelete:
		return c.handleDelete(ctx, e)
	case wire.MsgHistory:
		return c.handleHistory(ctx, e)
	case wire.MsgKeyPublish:
		return c.handleKeyPublish(ctx, e)
	case wire.MsgKeyFetch:
		return c.handleKeyFetch(ctx, e)
	case wire.MsgKeyFetchAll:
		return c.handleKeyFetchAll(ctx, e)
	case wire.MsgChatExport:
		return c.handleChatExport(ctx, e)
	case wire.MsgSecretSend:
		return c.handleSecretSend(ctx, e)
	case wire.MsgMediaInit:
		return c.handleMediaInit(ctx, e)
	case wire.MsgMediaFetch:
		return c.handleMediaFetch(ctx, e)
	case wire.MsgSearch:
		return c.handleSearch(ctx, e)
	default:
		return c.replyError(e.RequestID, wire.ErrProtocol, "unsupported message type")
	}
}
