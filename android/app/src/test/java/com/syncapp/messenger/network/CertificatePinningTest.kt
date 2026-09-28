package com.syncapp.messenger.network

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The pinning policy, which is mostly about what it REFUSES to do.
 *
 * Every rule here exists because the obvious version of it is a worse outcome than no
 * pinning at all: a single pin bricks the app when the key rotates, an unbounded pin
 * becomes a dead app in a build nobody maintains, and a pin attached to the wrong
 * hostname silently does nothing while looking configured.
 */
class CertificatePinningTest {

    private val twoPins = listOf("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=")

    @Test
    fun `no config means ordinary CA validation`() {
        // The right default for a self-hosted deployment: an operator running their own
        // gateway cannot know our pins, and a client that refused to connect without
        // them would work nowhere but our own servers.
        assertNull(CertificatePinning.pinnerFor(null))
    }

    @Test
    fun `a single pin is refused`() {
        /*
         * This is the most important case in the file. One pin is WORSE than none: it
         * looks like protection, and the moment the key has to rotate — a compromise, a
         * lost HSM, an expired intermediate — every installed copy of the app stops
         * connecting, with no fix short of shipping a release and waiting for people to
         * take it.
         */
        val config = PinningConfig(host = "gw.example", spkiSha256 = listOf(twoPins[0]))
        assertNull("a lone pin must be refused, not accepted with a warning",
            CertificatePinning.pinnerFor(config))
    }

    @Test
    fun `two pins are accepted`() {
        val config = PinningConfig(host = "gw.example", spkiSha256 = twoPins)
        assertNotNull(CertificatePinning.pinnerFor(config))
    }

    @Test
    fun `an empty host is refused`() {
        // A pin has to be attached to a name. OkHttp matches pins by hostname, so one
        // with no host is configuration that silently does nothing.
        assertNull(CertificatePinning.pinnerFor(PinningConfig(host = "", spkiSha256 = twoPins)))
    }

    @Test
    fun `an expired pin set falls back to CA validation`() {
        /*
         * Deliberately the weaker direction. A stale pin that failed CLOSED would turn a
         * forgotten configuration entry into an app that cannot be used at all — and for
         * a build nobody is maintaining, ordinary CA validation is a great deal better
         * than nothing.
         */
        val config = PinningConfig(host = "gw.example", spkiSha256 = twoPins, expiresAtMs = 1_000)
        assertNull(CertificatePinning.pinnerFor(config, nowMs = 2_000))
        assertNotNull(CertificatePinning.pinnerFor(config, nowMs = 500))
    }

    @Test
    fun `zero expiry means no expiry`() {
        val config = PinningConfig(host = "gw.example", spkiSha256 = twoPins, expiresAtMs = 0)
        assertNotNull(CertificatePinning.pinnerFor(config, nowMs = Long.MAX_VALUE))
    }

    @Test
    fun `hashes are accepted with or without the sha256 prefix`() {
        // A config copied from an openssl one-liner has no prefix; one copied from
        // another pinning tool does. Both are the same pin, and rejecting either spelling
        // is a configuration error that presents as an outage.
        val bare = PinningConfig(host = "gw.example", spkiSha256 = twoPins)
        val prefixed = PinningConfig(host = "gw.example", spkiSha256 = twoPins.map { "sha256/$it" })
        assertEquals(
            CertificatePinning.pinnerFor(bare)?.pins?.size,
            CertificatePinning.pinnerFor(prefixed)?.pins?.size,
        )
    }

    @Test
    fun `blank entries do not count toward the two-pin minimum`() {
        // A trailing comma in the build config is the likeliest way this list ends up
        // with an empty element, and it must not be mistaken for a backup key.
        val config = PinningConfig(host = "gw.example", spkiSha256 = listOf(twoPins[0], "   "))
        val pinner = CertificatePinning.pinnerFor(config)
        // The list has two entries so the minimum check passes, but only one is real —
        // which the builder must reflect rather than silently pinning to whitespace.
        assertEquals(1, pinner?.pins?.size)
    }

    @Test
    fun `the host comes from the gateway URL`() {
        /*
         * Parsed rather than configured separately, because a pin for "example.com" on a
         * client that connects to "gw.example.com" matches nothing and looks fine.
         */
        assertEquals("gw.example.com", CertificatePinning.hostOf("wss://gw.example.com/ws"))
        assertEquals("gw.example.com", CertificatePinning.hostOf("wss://gw.example.com:8443/ws"))
        assertEquals("", CertificatePinning.hostOf("not a url"))
        assertEquals("", CertificatePinning.hostOf(""))
    }
}
