package com.synapse.messenger.network.media

import com.synapse.messenger.core.IoDispatcher
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

/**
 * The bytes half of the media pipeline.
 *
 * The binary protocol carries only references: MEDIA_INIT returns an
 * HMAC-signed, expiring upload URL, the bytes go over plain HTTP PUT, and the
 * resulting `media_ref` is what a message carries. Two server-side rules shape
 * this client:
 *
 *  - **The signed size is enforced.** The upload handler rejects a body whose
 *    length differs from the size declared in MEDIA_INIT, so the ticket must be
 *    requested with the exact byte count we are about to send.
 *  - **Uploads are create-only.** A second PUT to the same ref answers 409 by
 *    design (nobody may swap content behind a ref recipients already hold), so a
 *    retry after a *successful* upload is treated as success, not failure.
 */
@Singleton
class MediaApi @Inject constructor(
    private val httpClient: OkHttpClient,
    @param:IoDispatcher private val io: CoroutineDispatcher,
) {
    suspend fun upload(uploadUrl: String, bytes: ByteArray, contentType: String) = withContext(io) {
        val body = bytes.toRequestBody(contentType.toMediaTypeOrNull())
        val request = Request.Builder().url(uploadUrl).put(body).build()
        httpClient.newCall(request).execute().use { response ->
            if (!response.isSuccessful && response.code != HTTP_CONFLICT) {
                throw IOException("media upload failed: ${response.code}")
            }
        }
    }

    private companion object {
        const val HTTP_CONFLICT = 409
    }
}
