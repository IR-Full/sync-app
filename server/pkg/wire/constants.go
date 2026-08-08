package wire

// QUICALPN is the ALPN protocol token for the Synapse QUIC transport.
const QUICALPN = "synapse-quic"

const (
	MsgReserved MsgType = 0

	// --- Handshake & capability negotiation ---
	MsgHello   MsgType = 1 // C→S: version, device, capabilities, optional resume token
	MsgWelcome MsgType = 2 // S→C: assigned session, negotiated caps, heartbeat interval

	// --- Authentication ---
	MsgAuth    MsgType = 3 // C→S: session token (or login credentials for MVP)
	MsgAuthOK  MsgType = 4 // S→C: auth accepted, user/device identity
	MsgAuthErr MsgType = 5 // S→C: auth rejected

	// --- Liveness ---
	MsgPing MsgType = 6 // either direction
	MsgPong MsgType = 7 // reply to Ping

	// --- Messaging ---
	MsgSend      MsgType = 8  // C→S: send a message (carries client dedup key)
	MsgSendAck   MsgType = 9  // S→C: server-assigned id + seq + timestamp for a Send
	MsgNew       MsgType = 10 // S→C: a message delivered to this device (fanout)
	MsgRead      MsgType = 11 // C→S: mark read up to a message id
	MsgReadUpd   MsgType = 12 // S→C: read receipt from another party
	MsgTyping    MsgType = 13 // C→S / S→C: typing indicator
	MsgPresence  MsgType = 14 // S→C: presence / last-seen update
	MsgEdit      MsgType = 15 // C→S: edit a message
	MsgDelete    MsgType = 16 // C→S: delete a message
	MsgHistory   MsgType = 17 // C→S: request backfill; S→C replies with MsgNew stream
	MsgHistoryOK MsgType = 18 // S→C: end-of-history marker with cursor

	// --- Media ---
	MsgMediaInit   MsgType = 20 // C→S: begin an upload, get an upload ticket
	MsgMediaTicket MsgType = 21 // S→C: upload URL + media_ref
	MsgMediaFetch  MsgType = 22 // C→S: request a signed download URL for a media_ref
	MsgMediaURL    MsgType = 23 // S→C: signed, expiring download URL

	// --- Search ---
	MsgSearch        MsgType = 60 // C→S: full-text query over the user's chats
	MsgSearchResults MsgType = 61 // S→C: ranked, permission-filtered hits

	// --- Secret (end-to-end encrypted) chats ---
	MsgKeyPublish  MsgType = 50 // C→S: publish this device's identity key + prekeys
	MsgKeyFetch    MsgType = 51 // C→S: fetch a peer device's prekey bundle
	MsgKeyBundle   MsgType = 52 // S→C: a peer's prekey bundle (for X3DH)
	MsgSecretSend  MsgType = 53 // C→S: opaque ciphertext to relay to a peer device
	MsgSecretRecv  MsgType = 54 // S→C: opaque ciphertext delivered from a peer device
	MsgKeyFetchAll MsgType = 55 // C→S: fetch ALL of a user's device bundles (multi-device)
	MsgKeyBundles  MsgType = 56 // S→C: every device bundle for a user

	// --- Admin / owner ---
	MsgChatExport       MsgType = 70 // C→S: owner/admin dump of a chat
	MsgChatExportResult MsgType = 71 // S→C: chat dump (meta + members + messages)

	// Reactions (80s block reserved for message-layer product features).
	MsgReact    MsgType = 80 // C→S: toggle an emoji reaction on a message
	MsgReactUpd MsgType = 81 // S→C: a reaction was added/removed by someone

	// Threads: fetch a reply branch under a root message.
	MsgThread   MsgType = 82 // C→S: request a thread's replies
	MsgThreadOK MsgType = 83 // S→C: end-of-thread-page marker with cursor

	// Polls: a question with fixed options, anchored to a message.
	MsgPollCreate MsgType = 84 // C→S: post a poll into a chat
	MsgPollVote   MsgType = 85 // C→S: vote for an option
	MsgPollClose  MsgType = 86 // C→S: creator stops accepting votes
	MsgPollState  MsgType = 87 // S→C: poll + current tally

	// Contacts: per-user address book with incremental sync + block list.
	MsgContactAdd    MsgType = 96  // C→S: add/rename a contact
	MsgContactRemove MsgType = 97  // C→S: remove a contact
	MsgContactSync   MsgType = 98  // C→S: fetch contacts changed since a cursor
	MsgContactList   MsgType = 99  // S→C: contacts page + new cursor
	MsgBlock         MsgType = 100 // C→S: block or unblock a user

	// Forwarding, scheduling, self-destruct.
	MsgForward        MsgType = 101 // C→S: copy a message into another chat
	MsgSchedule       MsgType = 102 // C→S: send a message later
	MsgScheduleList   MsgType = 103 // C→S: list my pending sends in a chat
	MsgScheduleCancel MsgType = 104 // C→S: cancel a pending send
	MsgScheduled      MsgType = 105 // S→C: pending-send list/confirmation

	// Pins are chat-wide; drafts are private and synced across the user's own
	// devices — same neighbourhood in the protocol, opposite visibility.
	MsgPin       MsgType = 106 // C→S: pin a message
	MsgUnpin     MsgType = 107 // C→S: unpin a message
	MsgPinList   MsgType = 108 // C→S: list a chat's pins
	MsgPinned    MsgType = 109 // S→C: the chat's current pin set
	MsgDraftSet  MsgType = 110 // C→S: save/clear a draft
	MsgDraftSync MsgType = 111 // C→S: fetch drafts changed since a cursor
	MsgDrafts    MsgType = 112 // S→C: draft page + cursor

	// Public handles, invite links, admin rights.
	MsgSetUsername  MsgType = 113 // C→S: claim/clear a chat.s public handle (owner)
	MsgInviteCreate MsgType = 114 // C→S: mint an invite link (admin)
	MsgInviteRevoke MsgType = 115 // C→S: kill a link (admin)
	MsgInviteList   MsgType = 116 // C→S: list a chat.s links (admin)
	MsgJoin         MsgType = 117 // C→S: join by invite code or @handle
	MsgSetRole      MsgType = 118 // C→S: promote/demote a member (owner)
	MsgInvites      MsgType = 119 // S→C: link list / join result

	// Chat creation. Groups and channels existed in the model from the start but
	// could only be made through the service API, so no client could create one —
	// the protocol could join, name, invite and administer a chat it had no way to
	// bring into existence.
	MsgChatCreate MsgType = 120 // C→S: create a group or channel
	MsgChatInfo   MsgType = 121 // S→C: the chat that was created

	// Push registration. Without it the notification path is inert: tokens were
	// stored on the device row that nothing ever wrote to.
	MsgPushToken MsgType = 122 // C→S: register/refresh this device's push token

	// Chat list. The membership was always in the store (ListUserChats) but had
	// no wire type, so a client could only learn about a chat by receiving
	// traffic in it — a fresh install started blank and stayed blank until
	// somebody wrote to it.
	MsgChatList MsgType = 123 // C→S: page through the chats I belong to
	MsgChats    MsgType = 124 // S→C: a page of chat summaries + cursor

	// Profiles. display_name could only be set at registration and was never
	// readable afterwards; there were no avatars at all. PROFILE_GET also takes
	// "@handle", which makes it the user lookup — clients previously resolved
	// handles through unrelated read-only messages.
	MsgProfileGet MsgType = 125 // C→S: read a user's public profile (id or @handle)
	MsgProfileSet MsgType = 126 // C→S: update MY display name / avatar
	MsgProfile    MsgType = 127 // S→C: a user's public profile

	// Calls & conferences (90s block). The server owns signaling only: media
	// never flows through it (peer-to-peer or via an SFU).
	MsgCallInvite  MsgType = 90 // C→S: start/join a call in a chat
	MsgCallAccept  MsgType = 91 // C→S: accept an incoming call
	MsgCallDecline MsgType = 92 // C→S: reject an incoming call
	MsgCallHangup  MsgType = 93 // C→S: leave/end a call
	MsgCallState   MsgType = 94 // S→C: room lifecycle + roster update
	MsgCallSignal  MsgType = 95 // both: opaque SDP/ICE relayed between devices

	// --- Transport control ---
	MsgTransportAck MsgType = 30 // either direction: cumulative ack of received Seq
	MsgResume       MsgType = 31 // C→S: resume a dropped session from last acked seq
	MsgResumeOK     MsgType = 32 // S→C: resume accepted, replay follows
	MsgError        MsgType = 40 // S→C: generic protocol/business error with code
)

const (
	ErrNone ErrorCode = 0

	// 1xxx — transport / protocol (client may retry after fixing framing).
	ErrProtocol      ErrorCode = 1000
	ErrBadFrame      ErrorCode = 1001
	ErrUnsupported   ErrorCode = 1002
	ErrPayloadTooBig ErrorCode = 1003
	ErrResumeExpired ErrorCode = 1004

	// 2xxx — auth / session (client must re-authenticate).
	ErrUnauthenticated ErrorCode = 2000
	ErrBadToken        ErrorCode = 2001
	ErrSessionRevoked  ErrorCode = 2002
	ErrDeviceUnknown   ErrorCode = 2003

	// 3xxx — authorization / business (do not retry as-is).
	ErrForbidden ErrorCode = 3000
	ErrNotFound  ErrorCode = 3001
	ErrConflict  ErrorCode = 3002
	ErrBadArg    ErrorCode = 3003

	// 4xxx — throttling (retry after backoff, honor RetryAfterMs).
	ErrRateLimited ErrorCode = 4000
	ErrFlood       ErrorCode = 4001

	// 5xxx — server (retry with backoff; safe because writes are idempotent).
	ErrInternal    ErrorCode = 5000
	ErrUnavailable ErrorCode = 5001
)

const (
	CapCompression   Cap = 1 << 0 // peer understands FlagCompressed (gzip) frames
	CapBatching      Cap = 1 << 1 // peer understands batched envelopes
	CapResume        Cap = 1 << 2 // peer supports session resume
	CapSecretChat    Cap = 1 << 3 // peer supports E2E secret chats (V2)
	CapTypingSignals Cap = 1 << 4 // peer wants typing/presence events
	CapZstd          Cap = 1 << 5 // peer understands FlagZstd (zstd+dict) frames
)
