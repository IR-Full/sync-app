export { Cap, CLIENT_CAPS, hasCap } from './caps'
export {
  SynapseClient,
  type ClientEvents,
  type ConnectionState,
  type Credentials,
  type DecodedEnvelope,
  type Session,
  type SynapseClientOptions,
} from './client'
export { decodeBody, encodeBody, hasBody } from './codec'
export { decodeEnvelope, encodeEnvelope, type Envelope } from './envelope'
export {
  ErrorCode,
  errorClass,
  isAuthError,
  isRetryable,
  ProtocolError,
  type ErrorClass,
} from './error-code'
export { decodeFrame, encodeFrame, Flag, FrameError } from './frame'
export { MsgType, msgTypeName } from './msg-type'
export type * as Wire from './generated/bodies'
export type { Encodable } from './generated/bodies'
