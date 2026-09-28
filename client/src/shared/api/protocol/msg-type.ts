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

  // Chat list: the only message that tells a fresh client which chats it is in.
  CHAT_LIST: 123,
  CHATS: 124,

  // Profiles. PROFILE_GET also takes "@handle", which makes it the user lookup.
  PROFILE_GET: 125,
  PROFILE_SET: 126,
  PROFILE: 127,

  /**
   * A message of ours reached a recipient's device — the step between "stored"
   * and "read". Pushed unsolicited; the body is a ReadUpdate, because
   * (chat_id, user_id, up_to_chat_seq) is exactly a delivery cursor.
   */
  DELIVERED: 128,

  /**
   * Account deletion. The store-mandated counterpart to registration: an app
   * that lets someone create an account in-app must let them destroy it in-app.
   * The password is re-confirmed because a session token lives on the device.
   */
  ACCOUNT_DELETE: 129,
  ACCOUNT_DELETED: 130,

  /**
   * Session management — "where am I signed in" and "sign me out of there".
   * Until these existed, logging out only discarded the token locally while the
   * session stayed valid on the server for the rest of its lifetime, so a lost
   * phone kept access.
   */
  SESSION_LIST: 131,
  SESSIONS: 132,
  SESSION_REVOKE: 133,
  SESSION_REVOKED: 134,

  /**
   * One backfill page in a single frame, instead of N NEW frames plus a
   * HISTORY_OK terminator. Sent only to peers that negotiated CAP_BATCHING.
   */
  HISTORY_PAGE: 135,

  /**
   * Per-user privacy: who may see last-seen and the avatar, and who may add
   * this account to a group. Readable and writable only for one's own account.
   */
  PRIVACY_GET: 136,
  PRIVACY_SET: 137,
  PRIVACY: 138,

  /**
   * Durable secret chats.
   *
   * SECRET_SEND was a pure relay: the server published to whichever nodes held
   * the recipient and discarded the count, so a recipient who happened to be
   * offline meant the ciphertext was dropped — no store, no push, and no reply to
   * the sender, which drew "sent" regardless. These four make it survivable:
   * SECRET_ACK says what the relay actually did, SECRET_SYNC collects what a
   * device missed, and SECRET_ACKED confirms so the server can drop it.
   */
  SECRET_ACK: 139,
  SECRET_SYNC: 140,
  SECRET_SYNCED: 141,
  SECRET_ACKED: 142,

  /**
   * Per-member chat settings. `muted` existed in the server schema from the first
   * migration with nothing reading it and no message to set it, so muting a chat
   * was impossible while looking supported. Pin and archive are the other two a
   * chat list needs and never had.
   */
  CHAT_FLAGS: 143,
  CHAT_FLAGS_SET: 144,

  /**
   * Account security. The password could not be CHANGED by any path — so a leaked
   * one meant a permanently lost account, since revoking sessions does not stop
   * whoever knows the password from signing in again — and there was no second
   * factor at all.
   */
  PASSWORD_CHANGE: 145,
  PASSWORD_CHANGED: 146,
  TOTP_SETUP: 147,
  TOTP_SETUP_INFO: 148,
  TOTP_CONFIRM: 149,
  TOTP_DISABLE: 150,
  TOTP_STATE: 151,

  /**
   * Billing. SUBSCRIPTION is both a reply and a server PUSH: a subscription
   * changes without the client asking (a payment settles, a period lapses), and
   * until the client hears about it it goes on offering features the server has
   * started refusing.
   */
  BILLING_PLANS: 152,
  BILLING_OFFERS: 153,
  BILLING_CHECKOUT: 154,
  BILLING_PAYMENT: 155,
  BILLING_STATUS: 156,
  SUBSCRIPTION: 157,
  BILLING_CANCEL: 158,

  /**
   * KEY_STATE answers KEY_PUBLISH with what the directory now holds for this
   * device: prekeys left, how old the stored signed prekey is, and how many of
   * the keys just sent were kept.
   *
   * It is the only channel for that information. One-time prekeys are consumed by
   * PEERS fetching bundles, so this device cannot observe its own balance
   * dropping; the local count only falls when a message actually decrypts with a
   * key, which misses every fetch that never turned into a message. Left to the
   * local count, a popular device runs dry and X3DH silently degrades from four
   * Diffie-Hellmans to three.
   */
  KEY_STATE: 159,
} as const

export type MsgType = (typeof MsgType)[keyof typeof MsgType]

const NAMES = new Map<number, string>(
  Object.entries(MsgType).map(([name, value]) => [value, name]),
)

/** Human-readable type name for logs and error messages. */
export function msgTypeName(type: number): string {
  return NAMES.get(type) ?? `UNKNOWN(${type})`
}
