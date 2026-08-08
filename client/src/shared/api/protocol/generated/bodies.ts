// GENERATED FILE — do not edit by hand.
// Source: server/proto/synapse/v1/body.proto (regenerate: npm run proto:gen)
//
// Every field is required here because the codec decodes with `defaults: true`,
// so proto3 scalars are always materialised. Use `Encodable<T>` when building a
// body to send — unset fields simply encode to their proto3 default.

export type Encodable<T> = {
  [K in keyof T]?: T[K] extends (infer U)[]
    ? Encodable<U>[]
    : T[K] extends object | null
      ? Encodable<NonNullable<T[K]>> | null
      : T[K]
}

export interface Hello {
  clientVersion: string
  deviceId: string
  platform: string
  caps: number
  resumeToken: string
}

export interface Welcome {
  serverVersion: string
  sessionId: string
  caps: number
  heartbeatMs: number
  maxInflight: number
  resumeSupported: boolean
}

export interface Auth {
  token: string
  username: string
  password: string
  register: boolean
}

export interface AuthOK {
  userId: string
  deviceId: string
  sessionId: string
  token: string
  resumeToken: string
}

export interface Send {
  chatId: string
  dedupKey: string
  text: string
  mediaRef: string
  replyTo: string
  attachment: Attachment | null
  ttlSeconds: number
}

export interface Attachment {
  kind: string
  mediaRef: string
  filename: string
  mime: string
  size: number
  durationMs: number
  waveform: number[]
  width: number
  height: number
  thumbRef: string
}

export interface SendAck {
  dedupKey: string
  messageId: string
  chatId: string
  chatSeq: number
  timestamp: number
  duplicate: boolean
}

export interface NewMessage {
  messageId: string
  chatId: string
  senderId: string
  chatSeq: number
  text: string
  mediaRef: string
  replyTo: string
  edited: boolean
  deleted: boolean
  timestamp: number
  attachment: Attachment | null
  threadRoot: string
  replyCount: number
  forward: ForwardOrigin | null
  expiresAt: number
}

export interface Thread {
  chatId: string
  rootId: string
  afterSeq: number
  limit: number
}

export interface ThreadOK {
  chatId: string
  rootId: string
  nextAfter: number
  done: boolean
}

export interface Read {
  chatId: string
  upToMessageId: string
  upToChatSeq: number
}

export interface ReadUpdate {
  chatId: string
  userId: string
  upToChatSeq: number
}

export interface Typing {
  chatId: string
  userId: string
  active: boolean
}

export interface React {
  chatId: string
  messageId: string
  emoji: string
}

export interface ReactUpdate {
  chatId: string
  messageId: string
  userId: string
  emoji: string
  added: boolean
  counts: Record<string, number>
}

export interface Presence {
  userId: string
  online: boolean
  lastSeenMs: number
}

export interface Edit {
  chatId: string
  messageId: string
  text: string
}

export interface Delete {
  chatId: string
  messageId: string
  forAll: boolean
}

export interface History {
  chatId: string
  beforeSeq: number
  limit: number
}

export interface HistoryOK {
  chatId: string
  nextBefore: number
  done: boolean
}

export interface Resume {
  resumeToken: string
  lastAckSeq: number
}

export interface ResumeOK {
  sessionId: string
  fromSeq: number
}

export interface Error {
  code: number
  message: string
  retryAfterMs: number
}

export interface MediaInit {
  filename: string
  contentType: string
  size: number
}

export interface MediaTicket {
  mediaRef: string
  uploadUrl: string
  expiresAt: number
}

export interface MediaFetch {
  mediaRef: string
}

export interface MediaURL {
  mediaRef: string
  downloadUrl: string
  expiresAt: number
}

export interface Search {
  query: string
  limit: number
}

export interface SearchHit {
  messageId: string
  chatId: string
  senderId: string
  seq: number
  text: string
}

export interface SearchResults {
  query: string
  hits: SearchHit[]
}

export interface KeyPublish {
  identityKey: string
  signingKey: string
  signedPrekey: string
  signedPrekeySig: string
  prekeys: string[]
}

export interface KeyFetch {
  userId: string
  deviceId: string
}

export interface KeyBundle {
  userId: string
  deviceId: string
  identityKey: string
  signingKey: string
  signedPrekey: string
  signedPrekeySig: string
  oneTimePrekey: string
}

export interface KeyBundles {
  userId: string
  bundles: KeyBundle[]
}

export interface SecretMsg {
  toUserId: string
  toDeviceId: string
  fromUserId: string
  fromDeviceId: string
  ratchetHeader: string
  ciphertext: string
}

export interface ChatExport {
  chatId: string
}

export interface ChatMember {
  userId: string
  role: string
  joinedAt: number
}

export interface ChatExportResult {
  chatId: string
  type: string
  title: string
  ownerId: string
  members: ChatMember[]
  messages: NewMessage[]
  done: boolean
}

export interface CallInvite {
  chatId: string
  kind: string
}

export interface CallAction {
  callId: string
}

export interface CallParticipant {
  userId: string
  deviceId: string
  state: string
}

export interface CallState {
  callId: string
  chatId: string
  initiatorId: string
  kind: string
  state: string
  participants: CallParticipant[]
}

export interface CallSignal {
  callId: string
  toUserId: string
  toDeviceId: string
  fromUserId: string
  fromDeviceId: string
  signalType: string
  payload: string
}

export interface PollCreate {
  chatId: string
  question: string
  options: string[]
  multiChoice: boolean
  anonymous: boolean
}

export interface PollVote {
  pollId: string
  option: number
}

export interface PollClose {
  pollId: string
}

export interface PollOption {
  index: number
  text: string
  votes: number
}

export interface PollState {
  pollId: string
  chatId: string
  messageId: string
  question: string
  options: PollOption[]
  totalVotes: number
  multiChoice: boolean
  anonymous: boolean
  closed: boolean
  myVotes: number[]
}

export interface ContactAdd {
  target: string
  name: string
}

export interface ContactRemove {
  target: string
}

export interface ContactSync {
  since: number
}

export interface Contact {
  userId: string
  name: string
  blocked: boolean
  updatedAt: number
}

export interface ContactList {
  contacts: Contact[]
  cursor: number
}

export interface Block {
  target: string
  blocked: boolean
}

export interface ForwardOrigin {
  chatId: string
  messageId: string
  senderId: string
}

export interface Forward {
  fromChatId: string
  messageId: string
  toChatId: string
  dedupKey: string
}

export interface Schedule {
  chatId: string
  text: string
  mediaRef: string
  attachment: Attachment | null
  replyTo: string
  ttlSeconds: number
  sendAt: number
}

export interface ScheduleList {
  chatId: string
}

export interface ScheduleCancel {
  id: string
}

export interface ScheduledItem {
  id: string
  chatId: string
  text: string
  sendAt: number
}

export interface Scheduled {
  items: ScheduledItem[]
}

export interface Pin {
  messageId: string
  pinnedBy: string
  pinnedAt: number
}

export interface PinAction {
  chatId: string
  messageId: string
}

export interface Pinned {
  chatId: string
  pins: Pin[]
}

export interface Draft {
  chatId: string
  text: string
  replyTo: string
}

export interface DraftSync {
  since: number
}

export interface DraftItem {
  chatId: string
  text: string
  replyTo: string
  updatedAt: number
}

export interface Drafts {
  drafts: DraftItem[]
  cursor: number
}

export interface SetUsername {
  chatId: string
  username: string
}

export interface InviteCreate {
  chatId: string
  expiresAt: number
  maxUses: number
}

export interface InviteRevoke {
  chatId: string
  code: string
}

export interface InviteList {
  chatId: string
}

export interface Join {
  code: string
  handle: string
}

export interface SetRole {
  chatId: string
  userId: string
  role: string
}

export interface InviteLink {
  code: string
  chatId: string
  expiresAt: number
  maxUses: number
  uses: number
}

export interface Invites {
  links: InviteLink[]
  joinedChat: string
}

export interface FanoutShard {
  body: NewMessage | null
  members: string[]
}

export interface ChatCreate {
  type: string
  title: string
  members: string[]
}

export interface ChatInfo {
  chatId: string
  type: string
  title: string
  ownerId: string
}

export interface PushToken {
  token: string
}
