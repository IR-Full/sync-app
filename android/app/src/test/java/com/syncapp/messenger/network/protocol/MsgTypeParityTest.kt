package com.syncapp.messenger.network.protocol

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * Holds this client's message types against the server's.
 *
 * The numbers here are not ours. They are `server/pkg/wire/constants.go`, and a
 * client that disagrees with it by one does not fail loudly — it decodes a
 * `PINNED` frame as a `DRAFTS` one, or drops a type it never learned about,
 * because the protocol's own extensibility rule is that an unknown type is
 * skipped in silence. That rule is what makes drift here expensive to notice
 * and cheap to introduce, which is the case for checking it mechanically.
 *
 * The comparison is on (number → name) PAIRS, and both halves come from the
 * server: the number from `constants.go`, the name from `MsgType.String()` in
 * `types.go`. An earlier version derived the name by screaming-snake-casing the
 * Go identifier instead, which is not the same thing and is not guessable —
 * `MsgTransportAck` renders as `T_ACK`. So this test passed while the client
 * logged `TRANSPORT_ACK` for a type the gateway calls `T_ACK`, which is exactly
 * the correlation a log name exists for. Reading both halves removes the guess.
 *
 * The check reads the Go files rather than a copy of them. A copy would be one
 * more thing to keep in step, and it would agree with itself forever.
 */
class MsgTypeParityTest {

    /**
     * Found by walking up rather than by a fixed relative path: Gradle runs unit
     * tests with the module directory as the working directory, which is one
     * level below the one a developer is usually standing in, and a path that
     * happens to work from one of them silently skips the whole test from the
     * other.
     */
    private val serverWire: File? = generateSequence(File(".").absoluteFile) { it.parentFile }
        .map { File(it, "server/pkg/wire") }
        .firstOrNull { File(it, "constants.go").isFile }

    /** Go identifier -> number. `Reserved` is 0, which is "no type at all". */
    private fun serverNumbers(): Map<String, Int> =
        Regex("""^\s*Msg(\w+)\s+MsgType = (\d+)""", RegexOption.MULTILINE)
            .findAll(File(checkNotNull(serverWire), "constants.go").readText())
            .associate { it.groupValues[1] to it.groupValues[2].toInt() }
            .filterKeys { it != "Reserved" }

    /** Go identifier -> the name the server puts in its logs and metric labels. */
    private fun serverNames(): Map<String, String> =
        Regex("""case Msg(\w+):\s*\n\s*return "([A-Z0-9_]+)"""")
            .findAll(File(checkNotNull(serverWire), "types.go").readText())
            .associate { it.groupValues[1] to it.groupValues[2] }

    /** number -> name, as the server defines the pair. */
    private fun serverTypes(): Map<Int, String> {
        val names = serverNames()
        return serverNumbers().entries.mapNotNull { (identifier, number) ->
            // A declared type with no branch in String() is the server's own
            // problem, and server/pkg/wire/names_test.go is what catches it. The
            // guard test below makes sure it cannot pass unnoticed here either.
            names[identifier]?.let { number to it }
        }.toMap()
    }

    /** number -> name, as this client defines the pair. */
    private fun clientTypes(): Map<Int, String> =
        clientConstants().entries.associate { (_, number) -> number to MsgType.name(number) }

    /** Kotlin constant name -> number, for the collision checks. */
    private fun clientConstants(): Map<String, Int> =
        MsgType::class.java.declaredFields
            .filter { it.type == Int::class.javaPrimitiveType }
            // The Compose compiler adds a synthetic `$stable` field to every
            // class it touches. Screaming snake case is what a wire constant
            // looks like and what a synthetic one does not.
            .filter { it.name.matches(Regex("[A-Z][A-Z0-9_]*")) }
            .associate { it.name to it.getInt(MsgType) }

    @Test
    fun `every server message type exists here under the same number and name`() {
        // Skipped rather than failed when the server tree is absent: this module
        // must stay buildable on its own, and a test that cannot run is not the
        // same as one that found a problem.
        assumeTrue("server/pkg/wire not present", serverWire != null)

        val client = clientTypes()
        val disagreements = serverTypes().mapNotNull { (number, name) ->
            when (val mine = client[number]) {
                name -> null
                null -> "$number ($name) is missing here"
                else -> "$number is \"$mine\" here, \"$name\" on the server"
            }
        }

        assertEquals("message types drifted from the server", emptyList<String>(), disagreements.sorted())
    }

    /**
     * Every type the server declares has a name in its own table. Asserted because
     * the parity check above TRUSTS it: a type the name table forgot would drop
     * silently out of the comparison rather than fail it.
     */
    @Test
    fun `the server names every type it declares`() {
        assumeTrue("server/pkg/wire not present", serverWire != null)

        val names = serverNames()
        val unnamed = serverNumbers().keys.filter { it !in names }

        assertEquals(
            "server types with no branch in MsgType.String(); this test cannot see them",
            emptyList<String>(),
            unnamed.sorted(),
        )
    }

    @Test
    fun `no two constants share a number`() {
        val byNumber = clientConstants().entries.groupBy({ it.value }, { it.key })
        val collisions = byNumber.filterValues { it.size > 1 }

        assertEquals("two names for one wire number", emptyMap<Int, List<String>>(), collisions)
    }

    /**
     * A type without a name renders as `UNKNOWN(109)` in a log, which is exactly
     * the case where somebody is reading the log to find out what arrived.
     */
    @Test
    fun `every type has a name`() {
        val unnamed = clientConstants().filterValues { MsgType.name(it).startsWith("UNKNOWN") }

        assertEquals("types missing from MsgType.name", emptyMap<String, Int>(), unnamed)
    }

    /**
     * Two types with one name is worse than a missing one: a metric or a log filter
     * keyed on the name silently merges two different messages.
     */
    @Test
    fun `no two types share a name`() {
        val byName = clientConstants().values.groupBy { MsgType.name(it) }
        val collisions = byName.filterValues { it.size > 1 }

        assertEquals("one name used for two types", emptyMap<String, List<Int>>(), collisions)
    }

    @Test
    fun `name falls back for a type this build does not know`() {
        assertEquals("UNKNOWN(9999)", MsgType.name(9999))
    }

    /**
     * The body table is derived from the gateway's handlers, and a type added to
     * [MsgType] without one decodes to null — the frame arrives, is understood
     * to be a `PINNED`, and carries nothing. Only the genuinely empty messages
     * and the bodiless control frames are allowed to be absent.
     */
    @Test
    fun `every type with a body is registered in the codec`() {
        val bodiless = setOf(
            // Control frames: the type is the whole message.
            "PING", "PONG", "TRANSPORT_ACK",
            // Empty protobuf messages: the request names itself and says nothing
            // more. SESSION_LIST asks for the caller's own sessions; PRIVACY_GET
            // asks for the caller's own settings, and a field that could name a
            // target would leak what the setting exists to protect.
            "SESSION_LIST", "PRIVACY_GET",
            // Same shape, three more: TOTP_SETUP asks the server to mint a secret and
            // names nothing; BILLING_STATUS and BILLING_CANCEL always concern the
            // caller's own subscription, and a field naming a target would be a field
            // for reading somebody else's.
            "TOTP_SETUP", "BILLING_STATUS", "BILLING_CANCEL",
        )

        val missing = clientConstants()
            .filterKeys { it !in bodiless }
            .filterValues { !BodyCodec.hasBody(it) }

        assertTrue("no body registered for: ${missing.keys.sorted()}", missing.isEmpty())
    }
}
