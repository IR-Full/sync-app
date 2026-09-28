package com.syncapp.messenger.datastore

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import com.syncapp.messenger.crypto.B64
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * What the directory says this device's bundle looks like, folded back into storage.
 *
 * This is the half the local pool cannot supply. One-time prekeys are consumed by PEERS
 * fetching bundles, and the pool here shrinks only when a message actually decrypts with
 * a key — which misses every fetch that never became a message: a peer that gave up, a
 * multi-device fan-out another device answered. Left to the local count the directory
 * empties while the device believes it is full, and every session started afterwards
 * silently uses three Diffie-Hellmans instead of four.
 *
 * Robolectric because [TokenCipher] reaches for `android.util.Base64`; the SDK is pinned
 * to 35 for the same reason as `TokenCipherTest`, and nothing here is version-specific.
 * With no AndroidKeyStore the cipher takes its documented passthrough, which is fine:
 * what is asserted is the bookkeeping, not the encryption.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SecretKeyStoreDirectoryStateTest {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private lateinit var file: File
    private lateinit var keys: SecretKeyStore

    @Before
    fun setUp() {
        file = File.createTempFile("secret-keys", ".preferences_pb").also { it.delete() }
        keys = SecretKeyStore(
            PreferenceDataStoreFactory.create(scope = scope) { file },
            TokenCipher(),
        )
    }

    @After
    fun tearDown() {
        file.delete()
    }

    private fun state(left: Int = 100, ageMs: Long = 0, accepted: Int = 0) =
        DirectoryKeyState(oneTimePreKeysLeft = left, signedPreKeyAgeMs = ageMs, accepted = accepted)

    @Test
    fun `accepted prekeys are marked as offered, so they are not published twice`() = runTest {
        val identity = keys.identity()
        val offered = identity.unpublishedPreKeys.map { B64.encode(it.publicKey) }
        assertEquals(identity.oneTimePreKeys.size, offered.size)

        keys.applyDirectoryState(state(accepted = offered.size), offered)

        // Publishing APPENDS on the server. Offering the same public key again files it
        // twice, and a one-time prekey stored twice can be handed to two peers — the one
        // thing it exists not to be.
        assertTrue(keys.identity().unpublishedPreKeys.isEmpty())
    }

    /**
     * A truncated frame keeps its LAST `accepted` keys: the directory appends and trims
     * from the front. Marking the first ones instead would retire exactly the keys it
     * dropped and re-offer the ones it kept — the duplicate this tracking prevents, in
     * the one case where it is certain to happen.
     */
    @Test
    fun `the keys the directory kept are retired, not the ones it dropped`() = runTest {
        val offered = keys.identity().unpublishedPreKeys.map { B64.encode(it.publicKey) }

        keys.applyDirectoryState(state(accepted = 3), offered)

        val stillPending = keys.identity().unpublishedPreKeys.map { B64.encode(it.publicKey) }
        assertEquals(offered.dropLast(3), stillPending)
    }

    @Test
    fun `a low directory count mints fresh keys however full the local pool is`() = runTest {
        val offered = keys.identity().unpublishedPreKeys.map { B64.encode(it.publicKey) }

        // The local pool is untouched — every key still here — while the directory has
        // served all but three. That divergence is the normal case, not an edge one.
        val republish =
            keys.applyDirectoryState(state(left = 3, accepted = offered.size), offered)

        assertTrue(republish)
        val pending = keys.identity().unpublishedPreKeys
        assertEquals(97, pending.size)
        // Fresh pairs, not the ones the directory has already served.
        val alreadyOffered = offered.toSet()
        assertTrue(pending.none { B64.encode(it.publicKey) in alreadyOffered })
    }

    @Test
    fun `a healthy bundle is not republished`() = runTest {
        val offered = keys.identity().unpublishedPreKeys.map { B64.encode(it.publicKey) }
        assertFalse(keys.applyDirectoryState(state(accepted = offered.size), offered))
    }

    /**
     * An unoffered remainder means the per-publish cap truncated the frame, NOT that the
     * directory needs anything. Republishing on that would send the remainder, have it
     * truncated again, and loop forever against a directory that is already full — so
     * the decision follows what the directory reports and nothing else.
     */
    @Test
    fun `unoffered keys alone do not trigger a republish`() = runTest {
        val offered = keys.identity().unpublishedPreKeys.map { B64.encode(it.publicKey) }

        assertFalse(keys.applyDirectoryState(state(accepted = 1), offered))
        assertEquals(offered.size - 1, keys.identity().unpublishedPreKeys.size)
    }

    /**
     * The directory is serving a signed prekey older than the rotation window. Either
     * this device never rotated, or it rotated and the publish never landed — and it
     * cannot tell which, because it cannot see what the directory holds. A republish
     * settles both: `maintain` rotates a genuinely old local key on the next pass, and a
     * fresh one simply goes out again.
     */
    @Test
    fun `a signed prekey past the rotation window is republished`() = runTest {
        keys.identity()
        val weekAndABit = 7L * 24 * 60 * 60 * 1000 + 1

        assertTrue(keys.applyDirectoryState(state(ageMs = weekAndABit), emptyList()))
    }

    @Test
    fun `the local pool is capped so refills cannot grow it without bound`() = runTest {
        keys.identity()

        // Five rounds of "the directory is empty": each mints a full batch while the old
        // private halves stay, since a first message may still be in flight against them.
        repeat(5) { keys.applyDirectoryState(state(left = 0), emptyList()) }

        assertEquals(256, keys.identity().oneTimePreKeys.size)
    }

    @Test
    fun `a consumed prekey takes its offered mark with it`() = runTest {
        val identity = keys.identity()
        val offered = identity.unpublishedPreKeys.map { B64.encode(it.publicKey) }
        keys.applyDirectoryState(state(accepted = offered.size), offered)

        val used = identity.oneTimePreKeys.first()
        keys.dropOneTimePreKey(used.publicKey)

        // Left behind, the mark would outlive its key and the set would grow by every
        // prekey this device ever offered.
        val after = keys.identity()
        assertEquals(offered.size - 1, after.publishedPreKeys.size)
        assertFalse(B64.encode(used.publicKey) in after.publishedPreKeys)
    }

    @Test
    fun `nothing is applied before an identity exists`() = runTest {
        assertFalse(keys.applyDirectoryState(state(), emptyList()))
    }
}
