package wire

// QUICALPN is the ALPN protocol token for the SyncApp QUIC transport.
const QUICALPN = "SyncApp-quic"

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

	// Delivery receipts. A SendAck proves the message is DURABLE, and a ReadUpd
	// proves it was READ, but nothing reported the step between them: fanout
	// pushed a message and told the sender nothing, so a client could only ever
	// draw two states. This is emitted by the gateway that actually wrote the
	// frame to a recipient's socket — not by the one that routed it — so it means
	// the bytes left the server, not merely that a node was notified.
	//
	// The body is a ReadUpdateBody: (chat_id, user_id, up_to_chat_seq) is exactly
	// the shape a delivery cursor needs, and per-chat seqs are monotonic, so a
	// client keeps the maximum and duplicates are harmless.
	MsgDelivered MsgType = 128 // S→C: a message of mine reached a recipient's device

	// Account deletion. An app that lets a user create an account in-app must let
	// them destroy it in-app (Apple 5.1.1(v), Google Play's account-deletion
	// policy) — until this existed the protocol could open an account but never
	// close one, and LOGOUT is not deletion: the row, the messages and the push
	// tokens all survived it.
	MsgAccountDelete  MsgType = 129 // C→S: erase MY account (password re-confirmed)
	MsgAccountDeleted MsgType = 130 // S→C: erasure done; the session is already dead

	// Session management. The auth service could always revoke a session and list
	// a user's sessions; nothing could ask it to, so "log out" only discarded the
	// token on the device while the session stayed valid until it expired — a lost
	// phone kept access for the whole TTL. These close that.
	MsgSessionList    MsgType = 131 // C→S: list every live session of MY account
	MsgSessions       MsgType = 132 // S→C: the session list (never includes tokens)
	MsgSessionRevoke  MsgType = 133 // C→S: kill one session, or all the others
	MsgSessionRevoked MsgType = 134 // S→C: how many sessions the revoke actually killed

	// One backfill page in ONE frame, instead of a hundred NEW frames plus a
	// terminator. Sent only to peers that negotiated CapBatching — which nothing
	// advertised until this existed, leaving the capability bit decorative.
	MsgHistoryPage MsgType = 135 // S→C: a page of history as a single frame

	// Privacy. Who may see last-seen, the avatar, and who may add this account to
	// a group. Readable and writable only for the caller's own account.
	MsgPrivacyGet MsgType = 136 // C→S: read MY settings
	MsgPrivacySet MsgType = 137 // C→S: replace MY settings
	MsgPrivacy    MsgType = 138 // S→C: the current settings

	// Durable secret chats. SECRET_SEND was pure relay: the gateway published to
	// whichever nodes held the recipient and discarded the count, so zero nodes —
	// recipient offline — meant the ciphertext was dropped, no push was queued,
	// and the sender was told nothing. These four close that: the relay now
	// reports what it did, undelivered ciphertext is held, and a device that
	// comes back collects it and says what it got.
	MsgSecretAck    MsgType = 139 // S→C: what the relay did with a SECRET_SEND
	MsgSecretSync   MsgType = 140 // C→S: give me the ciphertext I missed
	MsgSecretSynced MsgType = 141 // S→C: end of a sync page + cursor
	MsgSecretAcked  MsgType = 142 // C→S: I have stored these; drop them

	// Per-member chat settings. The `muted` column shipped in the first migration
	// and nothing ever read it: there was no message to set it and the
	// notification path never consulted it, so muting a chat was impossible while
	// the schema implied it was supported. Pin and archive are the other two
	// settings a chat list needs and never had.
	MsgChatFlags    MsgType = 143 // C→S: set MY mute/pin/archive for a chat
	MsgChatFlagsSet MsgType = 144 // S→C: the flags now in effect

	// Account security. The password could not be CHANGED — there was no message,
	// no service method and no store method — so a leaked one meant a permanently
	// lost account: revoking sessions does not stop whoever knows the password
	// from signing in again. And there was no second factor at all, which is the
	// other half of the same gap.
	MsgPasswordChange  MsgType = 145 // C→S: replace my password (old one re-confirmed)
	MsgPasswordChanged MsgType = 146 // S→C: done; how many other sessions were killed
	MsgTOTPSetup       MsgType = 147 // C→S: begin enrolment, get a secret + QR URI
	MsgTOTPSetupInfo   MsgType = 148 // S→C: the secret and its provisioning URI
	MsgTOTPConfirm     MsgType = 149 // C→S: prove a code works; enrols and returns recovery codes
	MsgTOTPDisable     MsgType = 150 // C→S: remove the factor (password + code)
	MsgTOTPState       MsgType = 151 // S→C: whether it is on, and codes remaining

	// Billing. A subscription is the one piece of account state that changes without
	// the client asking, so SUBSCRIPTION is a server PUSH as well as a reply: a
	// client still showing a tier the server has stopped honouring offers features
	// that get refused, which reads as the app breaking rather than as a plan
	// lapsing.
	MsgBillingPlans    MsgType = 152 // C→S: what can I buy, in my market
	MsgBillingOffers   MsgType = 153 // S→C: the plans, prices and payment methods
	MsgBillingCheckout MsgType = 154 // C→S: start a payment
	MsgBillingPayment  MsgType = 155 // S→C: where to pay (redirect or SBP QR)
	MsgBillingStatus   MsgType = 156 // C→S: my subscription and entitlements
	MsgSubscription    MsgType = 157 // S→C: subscription + entitlements (reply AND push)
	MsgBillingCancel   MsgType = 158 // C→S: stop renewing (access lasts the paid period)

	// KEY_STATE answers KEY_PUBLISH.
	//
	// Publishing used to be fire-and-forget, which left the publisher with nothing to
	// confirm against and — worse — no way to learn its own one-time prekey balance.
	// Those keys are consumed one per peer that starts a session, so a device that
	// runs dry silently drops to the weaker three-DH handshake and nobody finds out.
	// The count has to come back to the OWNER: the peer who fetches a bundle cannot
	// top up somebody else's keys.
	MsgKeyState MsgType = 159 // S→C: prekeys held, and how old the signed prekey is

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
	// ErrTwoFactorRequired: the password was right and a code is needed.
	//
	// Its own code, not ErrBadToken, because the client behaviour is completely
	// different: one sends the user back to a login screen, the other asks for six
	// digits on the screen they are already on. Reporting the second as the first
	// makes a working account look broken.
	//
	// 2004 and not 2003: that was already ErrDeviceUnknown, and Go happily compiles
	// two constants with the same value — so the collision was silent, and would have
	// shown up as a client logging out when it was asked for a code. The protocol
	// comment about never renumbering exists for exactly this.
	//
	// It belongs in the AUTH range because it only ever occurs BEFORE a session
	// exists: "re-authenticate" is literally what it is asking for.
	ErrTwoFactorRequired ErrorCode = 2004
	// ErrResumeReplayed: a resume token that had already been rotated away was
	// presented again, so the session has been ended.
	//
	// Distinct from ErrResumeExpired: expiry is routine, replay means someone else
	// had the token. Auth range is correct — the session really is gone.
	ErrResumeReplayed ErrorCode = 2005

	// 3xxx — authorization / business (do not retry as-is).
	ErrForbidden ErrorCode = 3000
	ErrNotFound  ErrorCode = 3001
	ErrConflict  ErrorCode = 3002
	ErrBadArg    ErrorCode = 3003
	// ErrTwoFactorInvalid: the code (or recovery code) did not verify.
	//
	// The BUSINESS range, not auth, and the reason is a client behaviour rather than
	// taxonomy: an auth-class error tells a client its session is void and to log in
	// again. This error also occurs on an AUTHENTICATED connection — confirming an
	// enrolment, or disabling the factor — where discarding the session would sign
	// somebody out for mistyping six digits.
	ErrTwoFactorInvalid ErrorCode = 3004
	// ErrPremiumRequired: the feature exists and this account tier does not include
	// it.
	//
	// Its own code rather than ErrForbidden, because the two mean different things to
	// a client: forbidden is final, while this one has an answer — show the upgrade
	// screen. Conflating them makes a purchasable feature look broken.
	//
	// Business range for the same reason as above: it arrives on a live session, and
	// an auth-class code would log the user out of the app instead of offering them
	// the plan.
	ErrPremiumRequired ErrorCode = 3005

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
	// CapSecretQueue: peer speaks the DURABLE secret-chat protocol — it acts on
	// SECRET_ACK, asks for what it missed with SECRET_SYNC, and confirms with
	// SECRET_ACKED.
	//
	// Undelivered ciphertext is queued for every recipient regardless of this
	// bit: holding it costs the same either way, and a peer that cannot ask for
	// it yet is no worse off than it was when the server dropped it. What the bit
	// decides is whether the server has any reason to expect the queue to drain —
	// which is what the sender's SECRET_ACK reports, and what stops a client that
	// will never collect from being told its message is on its way.
	CapSecretQueue Cap = 1 << 6
)
