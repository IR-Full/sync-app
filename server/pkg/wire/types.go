package wire

// MsgType identifies the semantic kind of an Envelope. Types are grouped by
// direction only for readability; the wire format is symmetric.
type MsgType uint16

func (t MsgType) String() string {
	switch t {
	case MsgHello:
		return "HELLO"
	case MsgWelcome:
		return "WELCOME"
	case MsgAuth:
		return "AUTH"
	case MsgAuthOK:
		return "AUTH_OK"
	case MsgAuthErr:
		return "AUTH_ERR"
	case MsgPing:
		return "PING"
	case MsgPong:
		return "PONG"
	case MsgSend:
		return "SEND"
	case MsgSendAck:
		return "SEND_ACK"
	case MsgNew:
		return "NEW"
	case MsgRead:
		return "READ"
	case MsgReadUpd:
		return "READ_UPD"
	case MsgDelivered:
		return "DELIVERED"
	case MsgTyping:
		return "TYPING"
	case MsgPresence:
		return "PRESENCE"
	case MsgEdit:
		return "EDIT"
	case MsgDelete:
		return "DELETE"
	case MsgHistory:
		return "HISTORY"
	case MsgHistoryOK:
		return "HISTORY_OK"
	case MsgMediaInit:
		return "MEDIA_INIT"
	case MsgMediaTicket:
		return "MEDIA_TICKET"
	case MsgMediaFetch:
		return "MEDIA_FETCH"
	case MsgMediaURL:
		return "MEDIA_URL"
	case MsgSearch:
		return "SEARCH"
	case MsgSearchResults:
		return "SEARCH_RESULTS"
	case MsgKeyPublish:
		return "KEY_PUBLISH"
	case MsgKeyFetch:
		return "KEY_FETCH"
	case MsgKeyBundle:
		return "KEY_BUNDLE"
	case MsgSecretSend:
		return "SECRET_SEND"
	case MsgSecretRecv:
		return "SECRET_RECV"
	case MsgKeyFetchAll:
		return "KEY_FETCH_ALL"
	case MsgKeyBundles:
		return "KEY_BUNDLES"
	case MsgChatList:
		return "CHAT_LIST"
	case MsgChats:
		return "CHATS"
	case MsgProfileGet:
		return "PROFILE_GET"
	case MsgProfileSet:
		return "PROFILE_SET"
	case MsgProfile:
		return "PROFILE"
	case MsgChatExport:
		return "CHAT_EXPORT"
	case MsgChatExportResult:
		return "CHAT_EXPORT_RESULT"
	case MsgTransportAck:
		return "T_ACK"
	case MsgResume:
		return "RESUME"
	case MsgResumeOK:
		return "RESUME_OK"
	case MsgError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ErrorCode is a stable, machine-readable error identifier carried in
// MsgError / MsgAuthErr bodies. Codes are grouped by class in decimal ranges so
// clients can react by range (e.g. 1xxx = retryable transport).
type ErrorCode uint32

// Capability flags negotiated in Hello/Welcome. Advertising via a bitset keeps
// negotiation forward-compatible: unknown bits are ignored, so old servers and
// new clients still agree on the intersection.
type Cap uint32
