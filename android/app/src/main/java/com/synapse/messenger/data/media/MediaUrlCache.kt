package com.synapse.messenger.data.media

import com.synapse.messenger.core.AppScope
import com.synapse.messenger.core.Outcome
import com.synapse.messenger.domain.repository.MediaRepository
import java.util.Collections
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * Media reference → signed URL, resolved on demand and shared by every screen.
 *
 * The protocol carries only references; a URL has to be asked for (MEDIA_FETCH) and
 * expires. Avatars make that awkward for a list: a chat list draws dozens of them,
 * they recur across screens, and a per-ViewModel cache would re-fetch the same URL
 * for the same person on every navigation. One app-scoped map instead, so the second
 * screen to ask already has the answer.
 */
@Singleton
class MediaUrlCache @Inject constructor(
    private val mediaRepository: MediaRepository,
    @param:AppScope private val scope: CoroutineScope,
) {
    private val _urls = MutableStateFlow<Map<String, String>>(emptyMap())
    val urls: StateFlow<Map<String, String>> = _urls.asStateFlow()

    /** Refs already being fetched, so N bubbles for one image cause one request. */
    private val inFlight = Collections.synchronizedSet(mutableSetOf<String>())

    fun request(mediaRef: String?) {
        if (mediaRef.isNullOrEmpty()) return
        if (_urls.value.containsKey(mediaRef)) return
        if (!inFlight.add(mediaRef)) return
        scope.launch {
            try {
                when (val outcome = mediaRepository.downloadUrl(mediaRef)) {
                    is Outcome.Success -> _urls.update { it + (mediaRef to outcome.value) }
                    // A missing picture is not worth an error banner; the next request
                    // for it will try again.
                    is Outcome.Failure -> Unit
                }
            } finally {
                inFlight.remove(mediaRef)
            }
        }
    }

    fun requestAll(refs: Iterable<String?>) = refs.forEach(::request)

    fun urlFor(mediaRef: String?): String? = mediaRef?.let { _urls.value[it] }
}
