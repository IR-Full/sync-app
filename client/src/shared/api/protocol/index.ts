export { Cap, CLIENT_CAPS, hasCap } from './caps'
export {
  SyncAppClient,
  type ClientEvents,
  type ConnectionState,
  type Credentials,
  type DecodedEnvelope,
  type Session,
  type SyncAppClientOptions,
} from './client'
export { decodeBody, encodeBody, hasBody } from './codec'
export { decodeEnvelope, encodeEnvelope, type Envelope } from './envelope'
export {
  errorClass,
  ErrorCode,
  isAuthError,
  isRetryable,
  ProtocolError,
  type ErrorClass,
} from './error-code'
export { decodeFrame, encodeFrame, Flag, FrameError } from './frame'
export type * as Wire from './generated/bodies'
export { MsgType, msgTypeName } from './msg-type'
