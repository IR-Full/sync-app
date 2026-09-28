package com.syncapp.messenger.network.protocol

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * Holds the hand-written bodies against `body.proto`.
 *
 * `Bodies.kt` is a hand transcription of a schema that lives somewhere else, and
 * the only thing protobuf puts on the wire is the field NUMBER. Swap two of them
 * and nothing fails: the encoder is happy, the decoder is happy, and a `Pinned`
 * arrives with the chat id in the pin list. There is no type information on the
 * wire to catch it and no test of behaviour that would notice, because both
 * sides of our own round-trip would be wrong in the same way.
 *
 * So this compares the numbers, in both directions, against the schema itself.
 * Reading the `.proto` rather than a copy of it is the point — a copy would
 * agree with itself forever.
 */
class BodySchemaParityTest {

    private val bodiesKt = File("src/main/java/com/syncapp/messenger/network/protocol/Bodies.kt")

    private val bodyProto: File? = generateSequence(File(".").absoluteFile) { it.parentFile }
        .map { File(it, "server/proto/syncapp/v1/body.proto") }
        .firstOrNull { it.isFile }

    /**
     * Kotlin classes whose names differ from the proto message they carry.
     *
     * Each is a deliberate rename rather than a mistake: Kotlin spells acronyms
     * in camel case, and `Error` and `Thread` would collide with types every
     * file already has in scope.
     */
    private val aliases = mapOf(
        "ProtocolError" to "Error",
        "AuthOk" to "AuthOK",
        "HistoryOk" to "HistoryOK",
        "ResumeOk" to "ResumeOK",
        "MediaUrl" to "MediaURL",
        "ThreadOk" to "ThreadOK",
    )

    /**
     * Kotlin classes with no proto message of their own.
     *
     * [Privacy] carries both `PrivacySet` and `Privacy`, which are the same three
     * fields; [SessionRevoke] and the rest do have messages and are not here.
     */
    private val kotlinOnly = setOf<String>()

    /**
     * Proto messages that are not envelope bodies.
     *
     * `FanoutShard` is an internal bus payload — it never crosses the wire to a
     * client. `SessionList` and `PrivacyGet` are empty by design, so there is
     * nothing to carry and nothing to get wrong.
     */
    private val notBodies = setOf(
        "FanoutShard", "SessionList", "PrivacyGet",
        // Empty by design: there is nothing to carry and nothing to get wrong. TOTPSetup
        // asks the server to mint a secret, and the two billing ones always concern the
        // caller's own subscription — a field naming a target would be a field for
        // reading somebody else's.
        "TOTPSetup", "BillingStatus", "BillingCancel",
    )

    private fun parseKotlin(): Map<String, Map<String, Int>> {
        val text = bodiesKt.readText()
        // Up to the closing paren at the start of a line: a field list contains
        // `@ProtoNumber(1)`, so "anything but a paren" stops at the first field.
        return Regex(
            """data class (\w+)\((.*?)^\)""",
            setOf(RegexOption.DOT_MATCHES_ALL, RegexOption.MULTILINE),
        )
            .findAll(text)
            .associate { match ->
                val fields = Regex("""@ProtoNumber\((\d+)\)[^\n]*?\bval (\w+)""")
                    .findAll(match.groupValues[2])
                    .associate { it.groupValues[2] to it.groupValues[1].toInt() }
                match.groupValues[1] to fields
            }
    }

    private fun parseProto(): Map<String, Map<String, Int>> {
        val text = checkNotNull(bodyProto).readText()
        return Regex("""\bmessage (\w+)\s*\{([^}]*)}""", RegexOption.DOT_MATCHES_ALL)
            .findAll(text)
            .associate { match ->
                val fields = Regex("""^\s*(?:repeated\s+|optional\s+)?(?:map<[^>]+>|[\w.]+)\s+(\w+)\s*=\s*(\d+)""", RegexOption.MULTILINE)
                    .findAll(match.groupValues[2])
                    .associate { it.groupValues[1] to it.groupValues[2].toInt() }
                match.groupValues[1] to fields
            }
    }

    /** `chatId` ↔ `chat_id`. */
    private fun snake(camel: String): String =
        camel.replace(Regex("([a-z0-9])([A-Z])"), "$1_$2").lowercase()

    @Test
    fun `every field number matches the schema`() {
        assumeTrue("server/proto/syncapp/v1/body.proto not present", bodyProto != null)

        val proto = parseProto()
        val problems = mutableListOf<String>()

        for ((className, fields) in parseKotlin()) {
            if (className in kotlinOnly) continue
            val messageName = aliases[className] ?: className
            val expected = proto[messageName]
            if (expected == null) {
                problems += "$className has no message $messageName in body.proto"
                continue
            }
            for ((property, number) in fields) {
                val wireName = snake(property)
                when (expected[wireName]) {
                    number -> Unit
                    null -> problems += "$messageName.$wireName does not exist in body.proto"
                    else -> problems +=
                        "$messageName.$wireName is $number here, ${expected[wireName]} in body.proto"
                }
            }
        }

        assertEquals("bodies drifted from body.proto", emptyList<String>(), problems)
    }

    /**
     * The other direction. A field the schema grew and this client never learned
     * about decodes to its default and reads as "the server did not send it" —
     * which is exactly how a missing `expires_at` becomes a message that never
     * self-destructs.
     */
    @Test
    fun `no schema field is missing from a body we carry`() {
        assumeTrue("server/proto/syncapp/v1/body.proto not present", bodyProto != null)

        val kotlin = parseKotlin()
        val byMessage = kotlin.mapKeys { (name, _) -> aliases[name] ?: name }
        val problems = mutableListOf<String>()

        var compared = 0
        for ((message, fields) in parseProto()) {
            if (message in notBodies) continue
            val ours = byMessage[message] ?: continue
            compared++
            val carried = ours.keys.map(::snake).toSet()
            for (field in fields.keys) {
                if (field !in carried) problems += "$message.$field is in body.proto and not here"
            }
        }

        assertEquals("body.proto grew fields this client ignores", emptyList<String>(), problems)
        // Messages with no class here are skipped, so a broken name mapping
        // would make this pass by comparing nothing at all.
        assertTrue("only $compared messages were compared", compared > 40)
    }

    @Test
    fun `the parser found something to check`() {
        // A regex that silently matches nothing would make both tests above pass
        // for the wrong reason, which is the classic way a schema check stops
        // checking.
        assertTrue("no bodies parsed", parseKotlin().size > 40)
    }
}
