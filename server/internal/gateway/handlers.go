package gateway

import (
	"context"
	"strings"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
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
	return c.authByPasswordWithCode(ctx, username, password, "", displayName, register)
}

// authByPasswordWithCode is authByPassword with a second factor.
//
// The code travels on the RETRY: a client cannot know in advance whether an
// account enforces a factor, and asking would make the protocol an oracle for
// which accounts are protected. So the flow is credentials → ErrTwoFactorRequired
// → credentials plus code.
func (c *conn) authByPasswordWithCode(ctx context.Context, username, password, code, displayName string, register bool) (*authIdentity, error) {
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
		sess, user, err = c.gw.svc.Auth.LoginWithCode(ctx, username, password, code, c.deviceID, platform)
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
		wire.MsgPin, wire.MsgUnpin, wire.MsgDraftSet, wire.MsgChatFlags,
		// Every account-security path runs argon2id: cheap to ask for, memory-hard
		// to serve. They are also the paths where an unmetered retry loop is a
		// credential-guessing loop.
		wire.MsgPasswordChange, wire.MsgTOTPSetup, wire.MsgTOTPConfirm, wire.MsgTOTPDisable,
		// A checkout reaches an EXTERNAL acquirer, so an unmetered loop makes this
		// server hammer somebody else's API. Cancel writes.
		wire.MsgBillingCheckout, wire.MsgBillingCancel,
		wire.MsgSetUsername, wire.MsgInviteCreate, wire.MsgInviteRevoke, wire.MsgJoin, wire.MsgSetRole,
		// SECRET_ACKED deletes rows, so it is metered with the other writes.
		wire.MsgSecretAcked,
		wire.MsgChatCreate, wire.MsgPushToken, wire.MsgProfileSet,
		// Session management writes to the session table and, for account deletion,
		// runs an argon2id verify. Both are cheap to ask for and expensive to serve.
		wire.MsgSessionRevoke, wire.MsgAccountDelete, wire.MsgSessionList,
		wire.MsgPrivacyGet, wire.MsgPrivacySet,
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

// amplifying reports whether a message type is a READ that costs the server more
// to answer than it costs the client to ask.
//
// These used to be outside flood control entirely, on the reasoning that a read
// changes nothing. That confuses "harmless" with "free". HISTORY is the clearest
// case: one small frame draws a database page and streams up to a hundred full
// message frames back, and a client can ask again immediately. CHAT_LIST, the
// *_SYNC pair and the *_LIST family are the same shape in miniature — a query
// whose answer is unbounded by the request that asked for it.
//
// Kept separate from stateChanging rather than merged into it, because the two
// budgets want different numbers: writes are rare and expensive to get wrong,
// reads are frequent and normal. One bucket for both would either throttle
// scrolling or stop metering sends.
func amplifying(t wire.MsgType) bool {
	switch t {
	case wire.MsgHistory, wire.MsgChatList, wire.MsgThread, wire.MsgRead,
		wire.MsgPinList, wire.MsgDraftSync, wire.MsgContactSync,
		wire.MsgInviteList, wire.MsgScheduleList,
		// Both read a row and a policy table per call. Cheap individually, and the
		// kind of thing a client polls in a loop if nothing stops it.
		wire.MsgBillingPlans, wire.MsgBillingStatus,
		// SECRET_SYNC is the same shape as HISTORY: one small frame draws a page
		// of full ciphertext frames back, and the client can ask again at once.
		wire.MsgSecretSync:
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
	if amplifying(e.Type) && !c.readLimit.Allow() {
		return c.replyErrorRetry(e.RequestID, wire.ErrFlood, "read rate limited", 1000)
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
	case wire.MsgChatFlags:
		return c.handleChatFlags(ctx, e)
	case wire.MsgPasswordChange:
		return c.handlePasswordChange(ctx, e)
	case wire.MsgBillingPlans:
		return c.handleBillingPlans(ctx, e)
	case wire.MsgBillingCheckout:
		return c.handleBillingCheckout(ctx, e)
	case wire.MsgBillingStatus:
		return c.handleBillingStatus(ctx, e)
	case wire.MsgBillingCancel:
		return c.handleBillingCancel(ctx, e)
	case wire.MsgTOTPSetup:
		return c.handleTOTPSetup(ctx, e)
	case wire.MsgTOTPConfirm:
		return c.handleTOTPConfirm(ctx, e)
	case wire.MsgTOTPDisable:
		return c.handleTOTPDisable(ctx, e)
	case wire.MsgTOTPState:
		return c.handleTOTPState(ctx, e)
	case wire.MsgProfileGet:
		return c.handleProfileGet(ctx, e)
	case wire.MsgProfileSet:
		return c.handleProfileSet(ctx, e)
	case wire.MsgSessionList:
		return c.handleSessionList(ctx, e)
	case wire.MsgSessionRevoke:
		return c.handleSessionRevoke(ctx, e)
	case wire.MsgAccountDelete:
		return c.handleAccountDelete(ctx, e)
	case wire.MsgPrivacyGet:
		return c.handlePrivacyGet(ctx, e)
	case wire.MsgPrivacySet:
		return c.handlePrivacySet(ctx, e)
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
	case wire.MsgSecretSync:
		return c.handleSecretSync(ctx, e)
	case wire.MsgSecretAcked:
		return c.handleSecretAcked(ctx, e)
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
