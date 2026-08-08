/**
 * Envelope message types — mirrors `server/pkg/wire/constants.go`.
 *
 * Numbers are allocated in blocks by area and are NEVER reused or renumbered
 * (an unknown type must be ignorable, not reinterpreted). Gaps below are
 * deliberate: they are the server's reserved ranges.
 */
export const MsgType = {
  RESERVED: 0,

  // Handshake & capability negotiation
  HELLO: 1,
  WELCOME: 2,

  // Authentication
  AUTH: 3,
  AUTH_OK: 4,
  AUTH_ERR: 5,

  // Liveness
  PING: 6,
  PONG: 7,

  // Messaging
  SEND: 8,
  SEND_ACK: 9,
  NEW: 10,
  READ: 11,
  READ_UPD: 12,
  TYPING: 13,
  PRESENCE: 14,
  EDIT: 15,
  DELETE: 16,
  HISTORY: 17,
  HISTORY_OK: 18,

  // Media
  MEDIA_INIT: 20,
  MEDIA_TICKET: 21,
  MEDIA_FETCH: 22,
  MEDIA_URL: 23,

  // Transport control
  T_ACK: 30,
  RESUME: 31,
  RESUME_OK: 32,
  ERROR: 40,

  // Secret (E2E) chats
  KEY_PUBLISH: 50,
  KEY_FETCH: 51,
  KEY_BUNDLE: 52,
  SECRET_SEND: 53,
  SECRET_RECV: 54,
  KEY_FETCH_ALL: 55,
  KEY_BUNDLES: 56,

  // Search
  SEARCH: 60,
  SEARCH_RESULTS: 61,

  // Admin / owner
  CHAT_EXPORT: 70,
  CHAT_EXPORT_RESULT: 71,

  // Reactions, threads, polls
  REACT: 80,
  REACT_UPD: 81,
  THREAD: 82,
  THREAD_OK: 83,
  POLL_CREATE: 84,
  POLL_VOTE: 85,
  POLL_CLOSE: 86,
  POLL_STATE: 87,

  // Calls (signaling only — media never traverses the server)
  CALL_INVITE: 90,
  CALL_ACCEPT: 91,
  CALL_DECLINE: 92,
  CALL_HANGUP: 93,
  CALL_STATE: 94,
  CALL_SIGNAL: 95,

  // Contacts & blocking
  CONTACT_ADD: 96,
  CONTACT_REMOVE: 97,
  CONTACT_SYNC: 98,
  CONTACT_LIST: 99,
  BLOCK: 100,

  // Forwarding, scheduling, self-destruct
  FORWARD: 101,
  SCHEDULE: 102,
  SCHEDULE_LIST: 103,
  SCHEDULE_CANCEL: 104,
  SCHEDULED: 105,

  // Pins (chat-wide) & drafts (private, synced across your own devices)
  PIN: 106,
  UNPIN: 107,
  PIN_LIST: 108,
  PINNED: 109,
  DRAFT_SET: 110,
  DRAFT_SYNC: 111,
  DRAFTS: 112,

  // Public handles, invite links, admin rights
  SET_USERNAME: 113,
  INVITE_CREATE: 114,
  INVITE_REVOKE: 115,
  INVITE_LIST: 116,
  JOIN: 117,
  SET_ROLE: 118,
  INVITES: 119,

  // Chat creation & push registration
  CHAT_CREATE: 120,
  CHAT_INFO: 121,
  PUSH_TOKEN: 122,
} as const

export type MsgType = (typeof MsgType)[keyof typeof MsgType]

const NAMES = new Map<number, string>(
  Object.entries(MsgType).map(([name, value]) => [value, name]),
)

/** Human-readable type name for logs and error messages. */
export function msgTypeName(type: number): string {
  return NAMES.get(type) ?? `UNKNOWN(${type})`
}
