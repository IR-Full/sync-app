package com.synapse.messenger.data.repository

import com.synapse.messenger.core.Outcome
import com.synapse.messenger.core.runOutcome
import com.synapse.messenger.domain.model.AttachmentKind
import com.synapse.messenger.domain.model.MessageAttachment
import com.synapse.messenger.domain.repository.MediaRepository
import com.synapse.messenger.network.SynapseGateway
import com.synapse.messenger.network.media.MediaApi
import com.synapse.messenger.network.protocol.MediaFetch
import com.synapse.messenger.network.protocol.MediaInit
import com.synapse.messenger.network.protocol.MediaTicket
import com.synapse.messenger.network.protocol.MediaUrl
import com.synapse.messenger.network.protocol.MsgType
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class MediaRepositoryImpl @Inject constructor(
    private val gateway: SynapseGateway,
    private val mediaApi: MediaApi,
) : MediaRepository {

    private data class CachedUrl(val url: String, val expiresAtMs: Long)

    /**
     * Signed URLs are cached until they expire.
     *
     * They are capability tokens with an expiry, so caching them is safe but must be
     * time-bounded — an expired URL answers 403, and Coil would surface that as a
     * broken image rather than retrying the fetch.
     */
    private val urlCache = ConcurrentHashMap<String, CachedUrl>()

    override suspend fun downloadUrl(mediaRef: String): Outcome<String> = runOutcome {
        val cached = urlCache[mediaRef]
        val now = System.currentTimeMillis()
        if (cached != null && cached.expiresAtMs - EXPIRY_MARGIN_MS > now) return@runOutcome cached.url

        val reply: MediaUrl = gateway.request(MsgType.MEDIA_FETCH, MediaFetch(mediaRef = mediaRef))
        urlCache[mediaRef] = CachedUrl(reply.downloadUrl, reply.expiresAt)
        reply.downloadUrl
    }

    /**
     * Uploads bytes and returns the attachment descriptor to put on a message.
     *
     * The declared size is part of what the gateway signs, and the upload handler
     * holds the body to exactly that many bytes — so the ticket must be minted for
     * the array we are about to send, not an estimate.
     */
    override suspend fun upload(
        bytes: ByteArray,
        filename: String,
        mime: String,
    ): Outcome<MessageAttachment> = runOutcome {
        val ticket: MediaTicket = gateway.request(
            MsgType.MEDIA_INIT,
            MediaInit(filename = filename, contentType = mime, size = bytes.size.toLong()),
        )
        mediaApi.upload(ticket.uploadUrl, bytes, mime)
        MessageAttachment(
            kind = kindFor(mime),
            mediaRef = ticket.mediaRef,
            filename = filename,
            mime = mime,
            size = bytes.size.toLong(),
        )
    }

    /**
     * The kind drives rendering on every client, so it is derived from the MIME type
     * rather than from the file extension a picker happened to report.
     */
    private fun kindFor(mime: String): AttachmentKind = when {
        mime.startsWith("image/") -> AttachmentKind.IMAGE
        mime.startsWith("video/") -> AttachmentKind.VIDEO
        mime.startsWith("audio/") -> AttachmentKind.VOICE
        else -> AttachmentKind.FILE
    }

    private companion object {
        /** Renew a little early so a URL cannot expire between handing it over and using it. */
        const val EXPIRY_MARGIN_MS = 30_000L
    }
}
