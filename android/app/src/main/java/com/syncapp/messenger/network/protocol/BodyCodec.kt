package com.syncapp.messenger.network.protocol

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.KSerializer
import kotlinx.serialization.protobuf.ProtoBuf
import kotlinx.serialization.serializer

/**
 * Encodes and decodes envelope bodies as protobuf.
 *
 * `encodeDefaults` stays off (the ProtoBuf default) so a field equal to its zero
 * value is omitted, exactly as proto3 requires — otherwise every empty optional
 * string we send would occupy bytes the Go decoder has to walk.
 *
 * The [BODY_TYPES] table is derived from the gateway handlers, not guessed. The
 * subtleties worth naming: MEDIA/CONTACT/PIN-style request and reply types often
 * reuse one message, `AUTH_ERR` carries an `Error` body like `ERROR` does, and
 * PING/PONG/TRANSPORT_ACK carry no body at all (hence absent, and [hasBody] false).
 */
@OptIn(ExperimentalSerializationApi::class)
object BodyCodec {
    private val protobuf = ProtoBuf { encodeDefaults = false }

    private val BODY_TYPES: Map<Int, KSerializer<*>> = mapOf(
        MsgType.HELLO to serializer<Hello>(),
        MsgType.WELCOME to serializer<Welcome>(),
        MsgType.AUTH to serializer<Auth>(),
        MsgType.AUTH_OK to serializer<AuthOk>(),
        MsgType.AUTH_ERR to serializer<ProtocolError>(),
        MsgType.SEND to serializer<Send>(),
        MsgType.SEND_ACK to serializer<SendAck>(),
        MsgType.NEW to serializer<NewMessage>(),
        MsgType.READ to serializer<Read>(),
        MsgType.READ_UPD to serializer<ReadUpdate>(),
        MsgType.TYPING to serializer<Typing>(),
        MsgType.PRESENCE to serializer<Presence>(),
        MsgType.EDIT to serializer<Edit>(),
        MsgType.DELETE to serializer<Delete>(),
        MsgType.HISTORY to serializer<History>(),
        MsgType.HISTORY_OK to serializer<HistoryOk>(),
        MsgType.MEDIA_INIT to serializer<MediaInit>(),
        MsgType.MEDIA_TICKET to serializer<MediaTicket>(),
        MsgType.MEDIA_FETCH to serializer<MediaFetch>(),
        MsgType.MEDIA_URL to serializer<MediaUrl>(),
        MsgType.RESUME to serializer<Resume>(),
        MsgType.RESUME_OK to serializer<ResumeOk>(),
        MsgType.ERROR to serializer<ProtocolError>(),
        MsgType.SEARCH to serializer<Search>(),
        MsgType.SEARCH_RESULTS to serializer<SearchResults>(),
        MsgType.CONTACT_ADD to serializer<ContactAdd>(),
        MsgType.CONTACT_REMOVE to serializer<ContactRemove>(),
        MsgType.CONTACT_SYNC to serializer<ContactSync>(),
        MsgType.CONTACT_LIST to serializer<ContactList>(),
        MsgType.BLOCK to serializer<Block>(),
        MsgType.JOIN to serializer<Join>(),
        MsgType.INVITES to serializer<Invites>(),
        MsgType.CHAT_CREATE to serializer<ChatCreate>(),
        MsgType.CHAT_INFO to serializer<ChatInfo>(),
        MsgType.PUSH_TOKEN to serializer<PushToken>(),
        MsgType.CHAT_LIST to serializer<ChatList>(),
        MsgType.CHATS to serializer<Chats>(),
        MsgType.PROFILE_GET to serializer<ProfileGet>(),
        MsgType.PROFILE_SET to serializer<ProfileSet>(),
        MsgType.PROFILE to serializer<Profile>(),
        // Same body as READ_UPD by design: a delivery cursor has the same three fields.
        MsgType.DELIVERED to serializer<ReadUpdate>(),
        MsgType.KEY_PUBLISH to serializer<KeyPublish>(),
        MsgType.KEY_FETCH to serializer<KeyFetch>(),
        MsgType.KEY_BUNDLE to serializer<KeyBundle>(),
        // KEY_FETCH_ALL reuses the KeyFetch body with an empty device id.
        MsgType.KEY_FETCH_ALL to serializer<KeyFetch>(),
        MsgType.KEY_BUNDLES to serializer<KeyBundles>(),
        MsgType.KEY_STATE to serializer<KeyState>(),
        MsgType.SECRET_SEND to serializer<SecretMsg>(),
        MsgType.SECRET_RECV to serializer<SecretMsg>(),
        MsgType.CHAT_EXPORT to serializer<ChatExport>(),
        MsgType.CHAT_EXPORT_RESULT to serializer<ChatExportResult>(),
        MsgType.REACT to serializer<React>(),
        MsgType.REACT_UPD to serializer<ReactUpdate>(),
        MsgType.THREAD to serializer<Thread>(),
        MsgType.THREAD_OK to serializer<ThreadOk>(),
        MsgType.POLL_CREATE to serializer<PollCreate>(),
        MsgType.POLL_VOTE to serializer<PollVote>(),
        MsgType.POLL_CLOSE to serializer<PollClose>(),
        MsgType.POLL_STATE to serializer<PollState>(),
        MsgType.CALL_INVITE to serializer<CallInvite>(),
        // ACCEPT/DECLINE/HANGUP are one body with one field: the call id.
        MsgType.CALL_ACCEPT to serializer<CallAction>(),
        MsgType.CALL_DECLINE to serializer<CallAction>(),
        MsgType.CALL_HANGUP to serializer<CallAction>(),
        MsgType.CALL_STATE to serializer<CallState>(),
        MsgType.CALL_SIGNAL to serializer<CallSignal>(),
        MsgType.FORWARD to serializer<Forward>(),
        MsgType.SCHEDULE to serializer<Schedule>(),
        MsgType.SCHEDULE_LIST to serializer<ScheduleList>(),
        MsgType.SCHEDULE_CANCEL to serializer<ScheduleCancel>(),
        MsgType.SCHEDULED to serializer<Scheduled>(),
        // PIN, UNPIN and PIN_LIST are the same request shape.
        MsgType.PIN to serializer<PinAction>(),
        MsgType.UNPIN to serializer<PinAction>(),
        MsgType.PIN_LIST to serializer<PinAction>(),
        MsgType.PINNED to serializer<Pinned>(),
        MsgType.DRAFT_SET to serializer<Draft>(),
        MsgType.DRAFT_SYNC to serializer<DraftSync>(),
        MsgType.DRAFTS to serializer<Drafts>(),
        MsgType.SET_USERNAME to serializer<SetUsername>(),
        MsgType.INVITE_CREATE to serializer<InviteCreate>(),
        MsgType.INVITE_REVOKE to serializer<InviteRevoke>(),
        MsgType.INVITE_LIST to serializer<InviteList>(),
        MsgType.SET_ROLE to serializer<SetRole>(),
        MsgType.ACCOUNT_DELETE to serializer<AccountDelete>(),
        MsgType.ACCOUNT_DELETED to serializer<AccountDeleted>(),
        // SESSION_LIST and PRIVACY_GET are empty messages: they name the request
        // and carry nothing, so they are absent here and hasBody is false.
        MsgType.SESSIONS to serializer<Sessions>(),
        MsgType.SESSION_REVOKE to serializer<SessionRevoke>(),
        MsgType.SESSION_REVOKED to serializer<SessionRevoked>(),
        MsgType.HISTORY_PAGE to serializer<HistoryPage>(),
        // PRIVACY_SET and PRIVACY are the same fields.
        MsgType.PRIVACY_SET to serializer<Privacy>(),
        MsgType.PRIVACY to serializer<Privacy>(),

        // Durable secret chats. SECRET_SYNCED answers BOTH SECRET_SYNC and
        // SECRET_ACKED, because both ask "how many, and is that all" — a second shape
        // for the same question would be two things to keep in step.
        MsgType.SECRET_ACK to serializer<SecretAck>(),
        MsgType.SECRET_SYNC to serializer<SecretSync>(),
        MsgType.SECRET_SYNCED to serializer<SecretSynced>(),
        MsgType.SECRET_ACKED to serializer<SecretAcked>(),

        MsgType.CHAT_FLAGS to serializer<ChatFlags>(),
        MsgType.CHAT_FLAGS_SET to serializer<ChatFlagsSet>(),

        // Account security. TOTP_SETUP is an empty message — it asks the server to mint
        // a secret and names nothing — so it is absent here, like SESSION_LIST.
        MsgType.PASSWORD_CHANGE to serializer<PasswordChange>(),
        MsgType.PASSWORD_CHANGED to serializer<PasswordChanged>(),
        MsgType.TOTP_SETUP_INFO to serializer<TOTPSetupInfo>(),
        MsgType.TOTP_CONFIRM to serializer<TOTPConfirm>(),
        MsgType.TOTP_DISABLE to serializer<TOTPDisable>(),
        MsgType.TOTP_STATE to serializer<TOTPState>(),

        // Billing. BILLING_STATUS and BILLING_CANCEL are empty for the same reason:
        // they always concern the caller's own subscription, and a field naming a
        // target would be a field for reading somebody else's.
        MsgType.BILLING_PLANS to serializer<BillingPlans>(),
        MsgType.BILLING_OFFERS to serializer<BillingOffers>(),
        MsgType.BILLING_CHECKOUT to serializer<BillingCheckout>(),
        MsgType.BILLING_PAYMENT to serializer<BillingPayment>(),
        MsgType.SUBSCRIPTION to serializer<Subscription>(),
    )

    private val EMPTY = ByteArray(0)

    /** Whether this envelope type carries a decodable body at all. */
    fun hasBody(msgType: Int): Boolean = BODY_TYPES.containsKey(msgType)

    @Suppress("UNCHECKED_CAST")
    fun encode(msgType: Int, body: Any?): ByteArray {
        if (body == null) return EMPTY
        val serializer = BODY_TYPES[msgType]
            ?: throw EnvelopeException("no protobuf body defined for ${MsgType.name(msgType)}")
        return protobuf.encodeToByteArray(serializer as KSerializer<Any>, body)
    }

    /** Decodes a body, or null for bodiless types and empty payloads. */
    fun decode(msgType: Int, body: ByteArray): Any? {
        if (body.isEmpty()) return null
        val serializer = BODY_TYPES[msgType] ?: return null
        return protobuf.decodeFromByteArray(serializer, body)
    }
}
