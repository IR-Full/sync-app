package com.syncapp.messenger.crypto

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pins this port against the Go implementation.
 *
 * Every value below was produced by `server/pkg/e2e` over fixed inputs. These
 * are not "does the code run" tests: a Double Ratchet that is internally
 * consistent but disagrees with the server by one byte works perfectly against
 * itself and fails against every real peer, with "decryption failed" as the only
 * symptom and no hint that serialisation is the cause.
 *
 * A plain JVM test: the crypto package deliberately depends on no Android
 * framework class, so none of this needs a simulated device to run.
 */
class CryptoInteropTest {

    /** Keys the Go vector generator used: b[i] = seed + i*7. */
    private fun fill(seed: Int): ByteArray = ByteArray(32) { (seed + it * 7).toByte() }

    /**
     * X25519 against Go's crypto/ecdh over fixed private keys.
     *
     * Both directions are checked because the agreement has to be symmetric AND
     * match the server; a library that derived public keys differently would
     * still pass a self-consistency test.
     */
    @Test
    fun `x25519 agrees with the Go implementation`() {
        val aPriv = fill(1)
        val bPriv = fill(9)
        val aPub = B64.decodeOrNull("yP7Kgb4ZbN8sreq/E8SQPXYy3OSVWqaLbl2a3vVOJhY=")!!
        val bPub = B64.decodeOrNull("cy7fq5I66k0o8YehEg1rHNCyrLM3Y+RQ1915E+EiYE0=")!!
        val shared = B64.decodeOrNull("CGcxUS5mwA7YS/egTtSH7JIfqPNCNEf/lYpjh+jiLDU=")!!

        assertArrayEquals(shared, Crypto.diffieHellman(aPriv, bPub))
        assertArrayEquals(shared, Crypto.diffieHellman(bPriv, aPub))
    }

    /**
     * The header is the AEAD's additional data, so these bytes must match Go's
     * `json.Marshal` character for character: declaration order, no whitespace,
     * `[]byte` as standard base64.
     */
    @Test
    fun `header serialises exactly as Go does`() {
        val header = RatchetHeader(dh = byteArrayOf(1, 2, 3), pn = 4, n = 7)
        assertEquals(
            """{"dh":"AQID","pn":4,"n":7}""",
            String(RatchetHeaderCodec.marshal(header), Charsets.UTF_8),
        )
    }

    @Test
    fun `header round-trips`() {
        val header = RatchetHeader(dh = fill(2), pn = 3, n = 9)
        val parsed = RatchetHeaderCodec.unmarshal(RatchetHeaderCodec.marshal(header))
        assertNotNull(parsed)
        assertArrayEquals(header.dh, parsed!!.dh)
        assertEquals(3, parsed.pn)
        assertEquals(9, parsed.n)
    }

    /**
     * The additional data is the bytes that TRAVELLED, not a re-encoding of the
     * values parsed out of them.
     *
     * Without this, interop rests on four independent implementations emitting
     * byte-identical canonical JSON forever, with nothing enforcing it and a
     * `DecryptException` — which reads exactly like a forgery — as the only
     * symptom when one of them drifts. Mirrors the Go tests in
     * `server/pkg/e2e/e2e_test.go` and the TypeScript ones in
     * `client/src/shared/lib/e2e/ratchet.test.ts`.
     */
    @Test
    fun `a peer's non-canonical encoding is kept verbatim`() {
        val foreign = """{ "n": 3, "pn": 1, "dh": "AQID" }""".toByteArray(Charsets.UTF_8)

        val parsed = RatchetHeaderCodec.unmarshal(foreign)
        assertNotNull(parsed)
        assertEquals(3, parsed!!.n)
        assertEquals(1, parsed.pn)
        assertArrayEquals(foreign, RatchetHeaderCodec.marshal(parsed))
    }

    @Test
    fun `a header rebuilt from values is encoded canonically`() {
        // copy() calls the primary constructor, so the derived header carries no
        // bytes of its own — which is what stops the parsed counters and the
        // authenticated bytes from ever disagreeing.
        val parsed = RatchetHeaderCodec.unmarshal(
            """{ "n": 3, "pn": 1, "dh": "AQID" }""".toByteArray(Charsets.UTF_8),
        )!!

        assertEquals(
            """{"dh":"AQID","pn":99,"n":3}""",
            String(RatchetHeaderCodec.marshal(parsed.copy(pn = 99)), Charsets.UTF_8),
        )
    }

    /**
     * The header arrives from a relay as attacker-influenced bytes. Every
     * rejection must be a null rather than a throw: an exception here would
     * escape into the frame handler and take down the connection over one bad
     * message.
     */
    @Test
    fun `malformed headers are rejected rather than thrown`() {
        for (bad in listOf("", "not json", """{"dh":"!!!","pn":1,"n":1}""", """{"pn":1}""")) {
            assertNull(bad, RatchetHeaderCodec.unmarshal(bad.toByteArray(Charsets.UTF_8)))
        }
    }

    /**
     * A negative counter must not become a huge gap. `n` is a uint32 on the Go
     * side, so a negative value is malformed input rather than a small number.
     */
    @Test
    fun `negative counters are rejected`() {
        val bad = """{"dh":"AQID","pn":-1,"n":0}""".toByteArray(Charsets.UTF_8)
        assertNull(RatchetHeaderCodec.unmarshal(bad))
    }

    @Test
    fun `safety number matches the Go implementation`() {
        val local = Safety.Identity("alice", fill(3), fill(5))
        val remote = Safety.Identity("bob", fill(11), fill(13))

        assertEquals(
            "47525 57927 53301 70585 70251 43205 47794 77283 53655 57607 96157 56186",
            Safety.number(local, remote),
        )
    }

    /**
     * The two endpoints disagree about which side is "local", so the
     * combination has to be canonical — otherwise each would read out a
     * different number and conclude they were under attack.
     */
    @Test
    fun `safety number is symmetric`() {
        val local = Safety.Identity("alice", fill(3), fill(5))
        val remote = Safety.Identity("bob", fill(11), fill(13))
        assertEquals(Safety.number(local, remote), Safety.number(remote, local))
    }

    @Test
    fun `safety number changes when an identity key changes`() {
        val local = Safety.Identity("alice", fill(3), fill(5))
        val remote = Safety.Identity("bob", fill(11), fill(13))
        val impostor = remote.copy(identityKey = fill(3))

        assertTrue(Safety.number(local, remote) != Safety.number(local, impostor))
    }

    @Test
    fun `base64 is the padded standard alphabet Go decodes`() {
        // Not url-safe: the gateway decodes prekeys with StdEncoding, and a
        // url-safe alphabet produces keys it silently rejects as malformed.
        val bytes = byteArrayOf(-5, -17, -1)
        assertEquals("++//", B64.encode(bytes))
        assertArrayEquals(bytes, B64.decodeOrNull("++//"))
    }
}
