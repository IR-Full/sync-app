package com.syncapp.messenger.crypto

import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.bouncycastle.crypto.params.Ed25519PrivateKeyParameters
import org.bouncycastle.crypto.params.X25519PrivateKeyParameters
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

/**
 * Replays `server/testdata/e2e/vectors.json`, produced by the Go implementation,
 * and requires this port to reproduce it byte for byte. With the ratchet keys
 * fixed, encryption is deterministic: matching Go's ciphertext means Go can
 * decrypt ours, and decrypting Go's means we can read Go's. The web and iOS
 * clients replay the same file.
 */
class VectorsTest {

    private val vectors: JsonObject by lazy {
        val file = generateSequence(File(".").absoluteFile) { it.parentFile }
            .map { File(it, "server/testdata/e2e/vectors.json") }
            .firstOrNull { it.isFile }
            // Failing rather than skipping: a vector test that quietly does
            // nothing is the exact drift it exists to catch.
            ?: error("server/testdata/e2e/vectors.json not found above ${File(".").absolutePath}")
        Json.parseToJsonElement(file.readText()).jsonObject
    }

    private val x3dh get() = vectors.getValue("x3dh").jsonObject
    private val conversation get() = vectors.getValue("conversation").jsonObject

    private fun JsonObject.str(key: String): String = getValue(key).jsonPrimitive.content
    private fun JsonObject.bytes(key: String): ByteArray = B64.decodeOrNull(str(key))!!
    private fun JsonObject.keyPair(key: String): KeyPair = getValue(key).jsonObject.asKeyPair()
    private fun JsonObject.asKeyPair(): KeyPair = KeyPair(bytes("private"), bytes("public"))
    private fun ratchetKeys(key: String): List<KeyPair> =
        conversation.getValue(key).jsonArray.map { it.jsonObject.asKeyPair() }

    /** Hands out recorded key pairs in order and counts how many were drawn. */
    private class Recorded(private val keys: List<KeyPair>) {
        var drawn = 0
            private set

        fun next(): KeyPair = keys.getOrNull(drawn++) ?: error("vector key source exhausted")
    }

    @Test
    fun `every public key derives from its private key`() {
        val fixed = listOf(
            "alice_identity", "alice_ephemeral", "bob_identity", "bob_signed_prekey", "bob_one_time_prekey",
        ).map { x3dh.keyPair(it) }
        val pairs = fixed + ratchetKeys("alice_ratchet_keys") + ratchetKeys("bob_ratchet_keys")
        for (kp in pairs) {
            val derived = X25519PrivateKeyParameters(kp.privateKey, 0).generatePublicKey().encoded
            assertEquals(B64.encode(kp.publicKey), B64.encode(derived))
        }
        val signing = x3dh.getValue("bob_signing").jsonObject
        val derivedSigning =
            Ed25519PrivateKeyParameters(signing.bytes("seed"), 0).generatePublicKey().encoded
        assertEquals(signing.str("public"), B64.encode(derivedSigning))
    }

    @Test
    fun `signs the prekey exactly as Go does`() {
        val seed = x3dh.getValue("bob_signing").jsonObject.bytes("seed")
        val sig = Crypto.signPreKey(seed, x3dh.keyPair("bob_signed_prekey").publicKey)
        assertEquals(x3dh.str("signed_prekey_signature"), B64.encode(sig))
    }

    @Test
    fun `agrees on the X3DH shared secret from both sides`() {
        val bundle = PreKeyBundle(
            identityKey = x3dh.keyPair("bob_identity").publicKey,
            signingKey = x3dh.getValue("bob_signing").jsonObject.bytes("public"),
            signedPreKey = x3dh.keyPair("bob_signed_prekey").publicKey,
            signedPreKeySig = x3dh.bytes("signed_prekey_signature"),
            oneTimePreKey = x3dh.keyPair("bob_one_time_prekey").publicKey,
        )
        val alice = InitiatorKeys(x3dh.keyPair("alice_identity"), x3dh.keyPair("alice_ephemeral"))
        val (sharedSecret, ephemeral) = X3dh.initiator(alice, bundle)
        assertEquals(x3dh.str("shared_secret"), B64.encode(sharedSecret))

        val bob = ResponderKeys(
            x3dh.keyPair("bob_identity"),
            x3dh.keyPair("bob_signed_prekey"),
            x3dh.keyPair("bob_one_time_prekey"),
        )
        val aliceIdentity = alice.identity.publicKey
        assertEquals(
            x3dh.str("shared_secret"),
            B64.encode(X3dh.responder(bob, aliceIdentity, ephemeral, true)),
        )
        assertEquals(
            x3dh.str("shared_secret_no_one_time"),
            B64.encode(X3dh.responder(bob, aliceIdentity, ephemeral, false)),
        )
    }

    @Test
    fun `reproduces the whole conversation byte for byte`() {
        val sharedSecret = x3dh.bytes("shared_secret")
        val signedPreKey = x3dh.keyPair("bob_signed_prekey")
        val aliceKeys = Recorded(ratchetKeys("alice_ratchet_keys"))
        val bobKeys = Recorded(ratchetKeys("bob_ratchet_keys"))
        val sessions = mapOf(
            "alice" to RatchetSession.initiator(sharedSecret, signedPreKey.publicKey, aliceKeys::next),
            "bob" to RatchetSession.responder(sharedSecret, signedPreKey, bobKeys::next),
        )
        val sent = mutableMapOf<String, JsonObject>()

        val steps: JsonArray = conversation.getValue("steps").jsonArray
        for ((i, element) in steps.withIndex()) {
            val step = element.jsonObject
            val op = step.str("op")
            val where = "step $i ($op ${step.str("id")})"
            if (op == "send") {
                val plaintext = step["plaintext"]?.jsonPrimitive?.content.orEmpty()
                val (header, ciphertext) =
                    sessions.getValue(step.str("from")).encrypt(plaintext.toByteArray(Charsets.UTF_8))
                val headerText = String(RatchetHeaderCodec.marshal(header), Charsets.UTF_8)
                assertEquals(where, step.str("header"), headerText)
                assertEquals(where, step.str("ciphertext"), B64.encode(ciphertext))
                sent[step.str("id")] = step
                continue
            }
            val message = sent.getValue(step.str("id"))
            val header = RatchetHeaderCodec.unmarshal(message.str("header").toByteArray(Charsets.UTF_8))!!
            val ciphertext = message.bytes("ciphertext")
            val session = sessions.getValue(step.str("to"))
            if (op == "forged") {
                ciphertext[ciphertext.size - 1] = (ciphertext[ciphertext.size - 1].toInt() xor 0x01).toByte()
                assertThrows(where, DecryptException::class.java) { session.decrypt(header, ciphertext) }
            } else {
                val expected = message["plaintext"]?.jsonPrimitive?.content.orEmpty()
                assertEquals(where, expected, String(session.decrypt(header, ciphertext), Charsets.UTF_8))
            }
        }
        assertEquals(ratchetKeys("alice_ratchet_keys").size, aliceKeys.drawn)
        assertEquals(ratchetKeys("bob_ratchet_keys").size, bobKeys.drawn)
    }

    @Test
    fun `computes the same safety numbers`() {
        for (element in vectors.getValue("safety").jsonArray) {
            val s = element.jsonObject
            val number = Safety.number(
                Safety.Identity(
                    s.str("local_id"), s.bytes("local_identity_key"), s.bytes("local_signing_key"),
                ),
                Safety.Identity(
                    s.str("remote_id"), s.bytes("remote_identity_key"), s.bytes("remote_signing_key"),
                ),
            )
            assertEquals(s.str("number"), number)
        }
    }
}
