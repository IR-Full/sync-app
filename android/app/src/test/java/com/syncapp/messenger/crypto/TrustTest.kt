package com.syncapp.messenger.crypto

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class TrustTest {
    private val ikA = B64.encode(ByteArray(32) { it.toByte() })
    private val skA = B64.encode(ByteArray(32) { (it + 100).toByte() })
    private val ikB = B64.encode(ByteArray(32) { (255 - it).toByte() })

    /**
     * The first sighting cannot be checked against anything — a directory that
     * lies from the very beginning is believed. That limit is inherent to TOFU,
     * not a gap in this implementation.
     */
    @Test
    fun `a never-seen device is first use`() {
        assertEquals(TrustVerdict.FirstUse, TrustStore().verify("u1", "d1", ikA, skA))
    }

    @Test
    fun `recorded keys are recognised`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)
        assertEquals(TrustVerdict.Known, store.verify("u1", "d1", ikA, skA))
    }

    /**
     * The whole point: a server that starts handing out its own keys is caught
     * on every existing conversation at once, rather than never.
     */
    @Test
    fun `a changed identity key is flagged`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)

        val verdict = store.verify("u1", "d1", ikB, skA)
        assertTrue(verdict is TrustVerdict.Changed)
        // The recorded identity comes back so the UI can say what it expected.
        assertEquals(ikA, (verdict as TrustVerdict.Changed).pinned.identityKey)
    }

    /** Both halves are pinned: forging one and keeping the other must not pass. */
    @Test
    fun `a changed signing key is flagged`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)
        assertTrue(store.verify("u1", "d1", ikA, ikB) is TrustVerdict.Changed)
    }

    /**
     * Pins are per DEVICE. Sharing one across a person's devices would mean a
     * reinstall on one phone invalidates the verification of another.
     */
    @Test
    fun `devices and users stay independent`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)

        assertEquals(TrustVerdict.FirstUse, store.verify("u1", "d2", ikB, skA))
        assertEquals(TrustVerdict.FirstUse, store.verify("u2", "d1", ikA, skA))
    }

    /** A reinstall is resolved by accepting, which replaces the record. */
    @Test
    fun `accepting a replacement resolves a change`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)
        store.accept("u1", "d1", ikB, skA)
        assertEquals(TrustVerdict.Known, store.verify("u1", "d1", ikB, skA))
    }

    @Test
    fun `a pin can be forgotten`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)
        store.forget("u1", "d1")
        assertEquals(TrustVerdict.FirstUse, store.verify("u1", "d1", ikA, skA))
    }

    /**
     * Pins that vanish on restart protect nothing: the attack they catch is a
     * key that changes between sessions.
     */
    @Test
    fun `a snapshot restores the same verdicts`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)

        val restored = TrustStore(store.snapshot())
        assertEquals(TrustVerdict.Known, restored.verify("u1", "d1", ikA, skA))
    }

    /** Malformed key material must read as a mismatch, never as a match. */
    @Test
    fun `unparseable key material counts as a change`() {
        val store = TrustStore()
        store.accept("u1", "d1", ikA, skA)
        assertTrue(store.verify("u1", "d1", "!!!not base64!!!", skA) is TrustVerdict.Changed)
    }
}
