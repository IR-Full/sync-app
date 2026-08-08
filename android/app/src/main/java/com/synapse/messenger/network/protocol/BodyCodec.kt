package com.synapse.messenger.network.protocol

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
 * PING/PONG/T_ACK carry no body at all (hence absent, and [hasBody] false).
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
