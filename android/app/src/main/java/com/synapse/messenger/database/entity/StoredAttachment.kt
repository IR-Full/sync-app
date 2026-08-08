package com.synapse.messenger.database.entity

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * Attachment metadata as stored in a single column.
 *
 * A separate storage shape rather than reusing the wire type: the protobuf body is
 * free to gain fields and renumber nothing, while this is a database schema whose
 * changes cost a migration. Ten nullable columns per message would be the
 * alternative, and nine of them are empty on every text message.
 */
@Serializable
data class StoredAttachment(
    val kind: String,
    val mediaRef: String,
    val filename: String = "",
    val mime: String = "",
    val size: Long = 0,
    val durationMs: Long = 0,
    val width: Int = 0,
    val height: Int = 0,
    val thumbRef: String = "",
    val waveform: List<Int> = emptyList(),
) {
    companion object {
        private val json = Json { ignoreUnknownKeys = true; encodeDefaults = false }

        fun encode(attachment: StoredAttachment?): String? =
            attachment?.let { json.encodeToString(serializer(), it) }

        fun decode(raw: String?): StoredAttachment? = raw
            ?.takeIf { it.isNotBlank() }
            ?.let { runCatching { json.decodeFromString(serializer(), it) }.getOrNull() }
    }
}
