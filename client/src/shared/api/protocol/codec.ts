/**
 * Body codec: protobuf, driven by the server's own schema.
 *
 * The gateway installs the protobuf codec in `pkg/wire/protocodec.go`'s package
 * `init()`, unconditionally — there is no JSON fallback to negotiate and no
 * runtime switch. So every envelope body on this connection is protobuf encoded
 * per `server/proto/synapse/v1/body.proto`.
 *
 * We load the schema as a pre-parsed JSON descriptor through `protobufjs/light`
 * (the runtime without the .proto parser), so the bundle carries the codec but
 * not a compiler.
 */
import { Root, type Type } from 'protobufjs/light'

import { descriptor } from './generated/descriptor'
import { MsgType, msgTypeName } from './msg-type'

const root = Root.fromJSON(descriptor as unknown as Parameters<typeof Root.fromJSON>[0])

/**
 * Which protobuf message carries each envelope type.
 *
 * Derived from the gateway handlers, not guessed. Two subtleties worth naming:
 * `PIN`/`UNPIN`/`PIN_LIST` all use `PinAction` (the `Pin` message is the item
 * inside a `Pinned` reply, not a request), and `KEY_FETCH_ALL` reuses
 * `KeyFetch`. Types with no body (`PING`, `PONG`, `T_ACK`) are absent.
 */
const BODY_TYPES = {
  [MsgType.HELLO]: 'Hello',
  [MsgType.WELCOME]: 'Welcome',
  [MsgType.AUTH]: 'Auth',
  [MsgType.AUTH_OK]: 'AuthOK',
  [MsgType.AUTH_ERR]: 'Error',
  [MsgType.SEND]: 'Send',
  [MsgType.SEND_ACK]: 'SendAck',
  [MsgType.NEW]: 'NewMessage',
  [MsgType.READ]: 'Read',
  [MsgType.READ_UPD]: 'ReadUpdate',
  [MsgType.TYPING]: 'Typing',
  [MsgType.PRESENCE]: 'Presence',
  [MsgType.EDIT]: 'Edit',
  [MsgType.DELETE]: 'Delete',
  [MsgType.HISTORY]: 'History',
  [MsgType.HISTORY_OK]: 'HistoryOK',
  [MsgType.MEDIA_INIT]: 'MediaInit',
  [MsgType.MEDIA_TICKET]: 'MediaTicket',
  [MsgType.MEDIA_FETCH]: 'MediaFetch',
  [MsgType.MEDIA_URL]: 'MediaURL',
  [MsgType.RESUME]: 'Resume',
  [MsgType.RESUME_OK]: 'ResumeOK',
  [MsgType.ERROR]: 'Error',
  [MsgType.KEY_PUBLISH]: 'KeyPublish',
  [MsgType.KEY_FETCH]: 'KeyFetch',
  [MsgType.KEY_BUNDLE]: 'KeyBundle',
  [MsgType.SECRET_SEND]: 'SecretMsg',
  [MsgType.SECRET_RECV]: 'SecretMsg',
  [MsgType.KEY_FETCH_ALL]: 'KeyFetch',
  [MsgType.KEY_BUNDLES]: 'KeyBundles',
  [MsgType.SEARCH]: 'Search',
  [MsgType.SEARCH_RESULTS]: 'SearchResults',
  [MsgType.CHAT_EXPORT]: 'ChatExport',
  [MsgType.CHAT_EXPORT_RESULT]: 'ChatExportResult',
  [MsgType.REACT]: 'React',
  [MsgType.REACT_UPD]: 'ReactUpdate',
  [MsgType.THREAD]: 'Thread',
  [MsgType.THREAD_OK]: 'ThreadOK',
  [MsgType.POLL_CREATE]: 'PollCreate',
  [MsgType.POLL_VOTE]: 'PollVote',
  [MsgType.POLL_CLOSE]: 'PollClose',
  [MsgType.POLL_STATE]: 'PollState',
  [MsgType.CALL_INVITE]: 'CallInvite',
  [MsgType.CALL_ACCEPT]: 'CallAction',
  [MsgType.CALL_DECLINE]: 'CallAction',
  [MsgType.CALL_HANGUP]: 'CallAction',
  [MsgType.CALL_STATE]: 'CallState',
  [MsgType.CALL_SIGNAL]: 'CallSignal',
  [MsgType.CONTACT_ADD]: 'ContactAdd',
  [MsgType.CONTACT_REMOVE]: 'ContactRemove',
  [MsgType.CONTACT_SYNC]: 'ContactSync',
  [MsgType.CONTACT_LIST]: 'ContactList',
  [MsgType.BLOCK]: 'Block',
  [MsgType.FORWARD]: 'Forward',
  [MsgType.SCHEDULE]: 'Schedule',
  [MsgType.SCHEDULE_LIST]: 'ScheduleList',
  [MsgType.SCHEDULE_CANCEL]: 'ScheduleCancel',
  [MsgType.SCHEDULED]: 'Scheduled',
  [MsgType.PIN]: 'PinAction',
  [MsgType.UNPIN]: 'PinAction',
  [MsgType.PIN_LIST]: 'PinAction',
  [MsgType.PINNED]: 'Pinned',
  [MsgType.DRAFT_SET]: 'Draft',
  [MsgType.DRAFT_SYNC]: 'DraftSync',
  [MsgType.DRAFTS]: 'Drafts',
  [MsgType.SET_USERNAME]: 'SetUsername',
  [MsgType.INVITE_CREATE]: 'InviteCreate',
  [MsgType.INVITE_REVOKE]: 'InviteRevoke',
  [MsgType.INVITE_LIST]: 'InviteList',
  [MsgType.JOIN]: 'Join',
  [MsgType.SET_ROLE]: 'SetRole',
  [MsgType.INVITES]: 'Invites',
  [MsgType.CHAT_CREATE]: 'ChatCreate',
  [MsgType.CHAT_INFO]: 'ChatInfo',
  [MsgType.PUSH_TOKEN]: 'PushToken',
} as const satisfies Partial<Record<number, string>>

const typeCache = new Map<number, Type>()

function lookup(msgType: number): Type | null {
  const cached = typeCache.get(msgType)
  if (cached) return cached
  const name = (BODY_TYPES as Record<number, string | undefined>)[msgType]
  if (!name) return null
  const type = root.lookupType(`synapse.v1.${name}`)
  typeCache.set(msgType, type)
  return type
}

const EMPTY = new Uint8Array(0)

/** Encodes a body for `msgType`. Bodiless types (PING/PONG/T_ACK) encode to zero bytes. */
export function encodeBody(msgType: number, body?: object | null): Uint8Array {
  if (body == null) return EMPTY
  const type = lookup(msgType)
  if (!type) {
    throw new Error(`no protobuf body defined for ${msgTypeName(msgType)}`)
  }
  const err = type.verify(body)
  if (err) {
    throw new Error(`invalid ${msgTypeName(msgType)} body: ${err}`)
  }
  return type.encode(type.create(body)).finish()
}

/**
 * Decodes a body into a plain object.
 *
 * `longs: Number` converts the 64-bit fields (chat_seq, timestamps, sizes) from
 * Long instances into plain numbers — safe here because ids are strings on this
 * wire, so nothing that could exceed 2^53 travels as an integer. `defaults:
 * true` materialises proto3 scalar defaults so callers never see `undefined`
 * where the schema promises a value.
 */
export function decodeBody<T>(msgType: number, body: Uint8Array): T {
  const type = lookup(msgType)
  if (!type) {
    throw new Error(`no protobuf body defined for ${msgTypeName(msgType)}`)
  }
  const message = type.decode(body)
  return type.toObject(message, {
    longs: Number,
    enums: String,
    bytes: String,
    defaults: true,
    arrays: true,
    objects: true,
  }) as T
}

/** Whether this envelope type carries a decodable body at all. */
export function hasBody(msgType: number): boolean {
  return lookup(msgType) !== null
}
