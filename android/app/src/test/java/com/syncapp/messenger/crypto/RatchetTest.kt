package com.syncapp.messenger.crypto

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The Double Ratchet, exercised through a real X3DH handshake.
 *
 * Hand-rolling a shared secret would test the ratchet against itself and miss
 * any disagreement about what the first DHr is — which is exactly the kind of
 * mistake that only shows up against a real peer.
 */
class RatchetTest {

    private class Pair2(val alice: RatchetSession, val bob: RatchetSession)

    private fun handshake(): Pair2 {
        val identity = Crypto.generateKeyPair()
        val signing = Crypto.generateSigningKeyPair()
        val signedPreKey = Crypto.generateKeyPair()

        val bundle = PreKeyBundle(
            identityKey = identity.publicKey,
            signingKey = signing.publicKey,
            signedPreKey = signedPreKey.publicKey,
            signedPreKeySig = Crypto.signPreKey(signing.privateKey, signedPreKey.publicKey),
        )

        val aliceKeys = InitiatorKeys(Crypto.generateKeyPair(), Crypto.generateKeyPair())
        val (sharedSecret, ephemeralPublic) = X3dh.initiator(aliceKeys, bundle)
        val bobSecret = X3dh.responder(
            ResponderKeys(identity, signedPreKey),
            aliceKeys.identity.publicKey,
            ephemeralPublic,
            usedOneTime = false,
        )

        return Pair2(
            RatchetSession.initiator(sharedSecret, signedPreKey.publicKey),
            RatchetSession.responder(bobSecret, signedPreKey),
        )
    }

    private fun send(from: RatchetSession, text: String) =
        from.encrypt(text.toByteArray(Charsets.UTF_8))

    private fun receive(to: RatchetSession, message: Pair<RatchetHeader, ByteArray>): String =
        String(to.decrypt(message.first, message.second), Charsets.UTF_8)

    @Test
    fun `round trips in both directions`() {
        val (alice, bob) = handshake().let { it.alice to it.bob }

        assertEquals("hello bob", receive(bob, send(alice, "hello bob")))
        // The reverse direction exercises the DH ratchet: Bob has no sending
        // chain until he has received once.
        assertEquals("hi alice", receive(alice, send(bob, "hi alice")))
    }

    @Test
    fun `carries a long conversation`() {
        val pair = handshake()
        repeat(20) { i ->
            assertEquals("a->b $i", receive(pair.bob, send(pair.alice, "a->b $i")))
            assertEquals("b->a $i", receive(pair.alice, send(pair.bob, "b->a $i")))
        }
    }

    @Test
    fun `opens messages that arrive out of order`() {
        val pair = handshake()
        val one = send(pair.alice, "one")
        val two = send(pair.alice, "two")
        val three = send(pair.alice, "three")

        assertEquals("three", receive(pair.bob, three))
        assertEquals("one", receive(pair.bob, one))
        assertEquals("two", receive(pair.bob, two))
    }

    @Test
    fun `identical plaintext produces different ciphertext`() {
        val pair = handshake()
        val (_, first) = send(pair.alice, "same")
        val (_, second) = send(pair.alice, "same")
        assertTrue(!first.contentEquals(second))
    }

    @Test
    fun `tampered ciphertext is rejected`() {
        val pair = handshake()
        val (header, ciphertext) = send(pair.alice, "secret")
        ciphertext[0] = (ciphertext[0].toInt() xor 0xFF).toByte()

        assertThrows(DecryptException::class.java) { pair.bob.decrypt(header, ciphertext) }
    }

    /**
     * The regression test for the defect the Go implementation shipped with.
     *
     * `decrypt` must not move any state before the AEAD authenticates: the
     * header is written by whoever sent the frame, and SECRET_SEND lets any
     * account address any device. Ratcheting first means one forged frame
     * permanently destroys a live session with the real peer.
     */
    @Test
    fun `a forged frame leaves the session able to talk to the real peer`() {
        val pair = handshake()
        assertEquals("first", receive(pair.bob, send(pair.alice, "first")))

        val attacker = Crypto.generateKeyPair()
        val forged = RatchetHeader(dh = attacker.publicKey, pn = 500, n = 500)
        assertThrows(DecryptException::class.java) {
            pair.bob.decrypt(forged, "not a real ciphertext".toByteArray(Charsets.UTF_8))
        }

        // Before the fix this threw: bob's root key and DHr had been replaced.
        assertEquals("second", receive(pair.bob, send(pair.alice, "second")))
    }

    /**
     * The header is authenticated as the bytes it travelled as. These two pin
     * that down end to end: a header that made the round trip through the wire
     * still opens, and one edited in flight does not.
     */
    @Test
    fun `a header that round-tripped through the wire still opens`() {
        val pair = handshake()
        val (header, ciphertext) = pair.alice.encrypt("hello".toByteArray(Charsets.UTF_8))

        val parsed = RatchetHeaderCodec.unmarshal(RatchetHeaderCodec.marshal(header))
        assertNotNull(parsed)
        assertEquals("hello", String(pair.bob.decrypt(parsed!!, ciphertext), Charsets.UTF_8))
    }

    @Test
    fun `a header edited in flight is rejected`() {
        // Carrying the bytes must not weaken the check it feeds: the counters and
        // the ratchet key are covered because they are inside the verified bytes.
        val pair = handshake()
        val (header, ciphertext) = pair.alice.encrypt("hello".toByteArray(Charsets.UTF_8))

        val onWire = String(RatchetHeaderCodec.marshal(header), Charsets.UTF_8)
        val forged = onWire.replace(""""n":0""", """"n":7""")
        assertTrue("the test did not actually change the header", forged != onWire)

        val parsed = RatchetHeaderCodec.unmarshal(forged.toByteArray(Charsets.UTF_8))
        assertNotNull(parsed)
        assertThrows(DecryptException::class.java) { pair.bob.decrypt(parsed!!, ciphertext) }
    }

    @Test
    fun `a forged frame stores no skipped keys`() {
        val pair = handshake()
        receive(pair.bob, send(pair.alice, "first"))
        val before = pair.bob.serialize().skipped.size

        repeat(20) {
            val attacker = Crypto.generateKeyPair()
            runCatching {
                pair.bob.decrypt(
                    RatchetHeader(dh = attacker.publicKey, pn = 999, n = 999),
                    "junk".toByteArray(Charsets.UTF_8),
                )
            }
        }

        assertEquals(before, pair.bob.serialize().skipped.size)
    }

    /**
     * A forgery on the CURRENT ratchet key takes the cheap in-order branch,
     * which holds its state in locals rather than staging a copy — the same
     * guarantee by a different mechanism, so it needs its own case.
     */
    @Test
    fun `a forgery on the fast path does not corrupt the session either`() {
        val pair = handshake()
        val real = send(pair.alice, "real")

        assertThrows(DecryptException::class.java) {
            pair.bob.decrypt(real.first, "wrong ciphertext".toByteArray(Charsets.UTF_8))
        }
        assertEquals("real", receive(pair.bob, real))
    }

    /**
     * MAX_SKIP bounds ONE call; every DH ratchet step restarts the count, so
     * without a total bound the retained map grows forever — and it is
     * persisted, so the growth would outlive the process.
     */
    @Test
    fun `the skipped-key store stays bounded across ratchet steps`() {
        val pair = handshake()

        repeat(3) {
            var last = send(pair.alice, "filler")
            repeat(900) { last = send(pair.alice, "filler") }
            receive(pair.bob, last)
        }

        assertTrue(pair.bob.serialize().skipped.size <= 2000)
    }

    @Test
    fun `a session survives serialisation`() {
        val pair = handshake()
        receive(pair.bob, send(pair.alice, "before"))

        val restored = RatchetSession.deserialize(pair.bob.serialize())
        assertNotNull(restored)
        assertEquals("after", receive(restored!!, send(pair.alice, "after")))
    }

    @Test
    fun `a serialised session round-trips its eviction order`() {
        val pair = handshake()
        send(pair.alice, "skipped")
        receive(pair.bob, send(pair.alice, "arrived"))

        val state = pair.bob.serialize()
        assertEquals(state.skipped.keys.toList(), state.skippedOrder)

        val restored = RatchetSession.deserialize(state)
        assertEquals(state.skippedOrder, restored!!.serialize().skippedOrder)
    }

    @Test
    fun `malformed stored state is rejected rather than crashing`() {
        val bad = SerializedSession(
            dhsPrivate = "!!!not base64",
            dhsPublic = "!!!",
            rootKey = "!!!",
        )
        assertNull(RatchetSession.deserialize(bad))
    }

    /**
     * An unsigned bundle must be refused. Verifying only when the fields are
     * present hands the attacker the switch: a hostile directory never has to
     * forge anything, it just sends nothing.
     */
    @Test
    fun `x3dh refuses a bundle with no signature`() {
        val identity = Crypto.generateKeyPair()
        val signing = Crypto.generateSigningKeyPair()
        val signedPreKey = Crypto.generateKeyPair()
        val attacker = Crypto.generateKeyPair()
        val keys = InitiatorKeys(Crypto.generateKeyPair(), Crypto.generateKeyPair())

        val unsigned = PreKeyBundle(
            identityKey = identity.publicKey,
            signingKey = ByteArray(0),
            signedPreKey = attacker.publicKey,
            signedPreKeySig = ByteArray(0),
        )
        assertThrows(BadPreKeySignatureException::class.java) { X3dh.initiator(keys, unsigned) }

        // And a signature over a DIFFERENT key is refused too — the substitution
        // the signature exists to catch.
        val substituted = PreKeyBundle(
            identityKey = identity.publicKey,
            signingKey = signing.publicKey,
            signedPreKey = attacker.publicKey,
            signedPreKeySig = Crypto.signPreKey(signing.privateKey, signedPreKey.publicKey),
        )
        assertThrows(BadPreKeySignatureException::class.java) { X3dh.initiator(keys, substituted) }
    }

    @Test
    fun `x3dh with a one-time prekey reaches the same secret`() {
        val identity = Crypto.generateKeyPair()
        val signing = Crypto.generateSigningKeyPair()
        val signedPreKey = Crypto.generateKeyPair()
        val oneTime = Crypto.generateKeyPair()

        val bundle = PreKeyBundle(
            identityKey = identity.publicKey,
            signingKey = signing.publicKey,
            signedPreKey = signedPreKey.publicKey,
            signedPreKeySig = Crypto.signPreKey(signing.privateKey, signedPreKey.publicKey),
            oneTimePreKey = oneTime.publicKey,
        )

        val keys = InitiatorKeys(Crypto.generateKeyPair(), Crypto.generateKeyPair())
        val (aliceSecret, ephemeral) = X3dh.initiator(keys, bundle)
        val bobSecret = X3dh.responder(
            ResponderKeys(identity, signedPreKey, oneTime),
            keys.identity.publicKey,
            ephemeral,
            usedOneTime = true,
        )

        assertArrayEquals(aliceSecret, bobSecret)
    }
}
