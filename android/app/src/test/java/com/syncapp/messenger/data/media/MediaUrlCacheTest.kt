package com.syncapp.messenger.data.media

import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.usecase.FakeMediaRepository
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * One app-scoped media-ref → URL map, shared by every screen.
 *
 * The protocol carries only references, and a URL has to be asked for. That is
 * awkward for a list: a chat list draws dozens of avatars, the same people recur
 * across screens, and each Compose recomposition asks again. Both of the
 * properties below exist to stop that turning into a request storm — and neither
 * is visible from a single-call test.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class MediaUrlCacheTest {

    private fun cache(scope: TestScope, media: FakeMediaRepository) =
        MediaUrlCache(media, scope)

    @Test
    fun `resolves a reference to a url`() = runTest {
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.request("media-1")
        advanceUntilIdle()

        assertEquals("https://media.test/media-1", cache.urlFor("media-1"))
    }

    @Test
    fun `reports no url before one has been resolved`() = runTest {
        val cache = cache(this, FakeMediaRepository())
        assertNull(cache.urlFor("media-1"))
    }

    @Test
    fun `publishes the url on its flow`() = runTest {
        val cache = cache(this, FakeMediaRepository())

        cache.request("media-1")
        advanceUntilIdle()

        assertEquals(mapOf("media-1" to "https://media.test/media-1"), cache.urls.value)
    }

    @Test
    fun `ignores a null reference`() = runTest {
        // Most messages have no media at all, so this is the common path.
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.request(null)
        advanceUntilIdle()

        assertTrue(media.requested.isEmpty())
    }

    @Test
    fun `ignores an empty reference`() = runTest {
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.request("")
        advanceUntilIdle()

        assertTrue(media.requested.isEmpty())
    }

    @Test
    fun `urlFor tolerates a null reference`() = runTest {
        assertNull(cache(this, FakeMediaRepository()).urlFor(null))
    }

    @Test
    fun `does not re-fetch a reference it already holds`() = runTest {
        // The second screen to show a person's avatar should already have it;
        // re-fetching would spend a frame and a signed URL for nothing.
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.request("media-1")
        advanceUntilIdle()
        cache.request("media-1")
        advanceUntilIdle()

        assertEquals(listOf("media-1"), media.requested)
    }

    /**
     * The in-flight set is what turns N simultaneous askers into one request. A
     * chat list laying out thirty rows asks for the same avatar many times within
     * one frame, long before the first answer arrives — so a cache that only
     * checked the *resolved* map would issue thirty MEDIA_FETCH frames and get
     * the connection throttled.
     */
    @Test
    fun `collapses concurrent requests for the same reference into one`() = runTest {
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        repeat(30) { cache.request("media-1") }
        advanceUntilIdle()

        assertEquals(listOf("media-1"), media.requested)
    }

    @Test
    fun `fetches distinct references independently`() = runTest {
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.request("media-1")
        cache.request("media-2")
        advanceUntilIdle()

        assertEquals(setOf("media-1", "media-2"), media.requested.toSet())
        assertEquals("https://media.test/media-2", cache.urlFor("media-2"))
    }

    @Test
    fun `requestAll fetches every reference in a batch`() = runTest {
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.requestAll(listOf("media-1", "media-2", "media-3"))
        advanceUntilIdle()

        assertEquals(setOf("media-1", "media-2", "media-3"), media.requested.toSet())
    }

    @Test
    fun `requestAll skips nulls and blanks in a batch`() = runTest {
        // A chat list maps over rows whose avatar ref is usually absent.
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.requestAll(listOf("media-1", null, "", "media-1"))
        advanceUntilIdle()

        assertEquals(listOf("media-1"), media.requested)
    }

    @Test
    fun `a failed fetch leaves no url and no error`() = runTest {
        // A missing picture is not worth an error banner — the avatar simply
        // falls back to its monogram.
        val media = FakeMediaRepository().apply {
            downloadUrlResult = { Outcome.Failure(AppError.Offline) }
        }
        val cache = cache(this, media)

        cache.request("media-1")
        advanceUntilIdle()

        assertNull(cache.urlFor("media-1"))
        assertTrue(cache.urls.value.isEmpty())
    }

    @Test
    fun `a failed fetch can be retried`() = runTest {
        // The in-flight marker has to be released in a finally, or one offline
        // moment would permanently blacklist that reference for the process.
        var attempts = 0
        val media = FakeMediaRepository().apply {
            downloadUrlResult = { ref ->
                attempts++
                if (attempts == 1) Outcome.Failure(AppError.Offline)
                else Outcome.Success("https://media.test/$ref")
            }
        }
        val cache = cache(this, media)

        cache.request("media-1")
        advanceUntilIdle()
        cache.request("media-1")
        advanceUntilIdle()

        assertEquals("https://media.test/media-1", cache.urlFor("media-1"))
        assertEquals(2, attempts)
    }

    @Test
    fun `keeps already-resolved urls when a later fetch fails`() = runTest {
        val media = FakeMediaRepository()
        val cache = cache(this, media)

        cache.request("media-1")
        advanceUntilIdle()

        media.downloadUrlResult = { Outcome.Failure(AppError.Offline) }
        cache.request("media-2")
        advanceUntilIdle()

        assertEquals("https://media.test/media-1", cache.urlFor("media-1"))
    }
}
