package com.syncapp.messenger.crypto

import kotlinx.serialization.Serializable

/**
 * Trust-on-first-use pinning of peer identity keys.
 *
 * A safety number lets two people DETECT a swapped identity key — if they think
 * to compare one. Almost nobody does. Pinning is the half that does not depend
 * on anyone remembering: the first time a peer device is seen its identity keys
 * are recorded, and every later session with that device is checked against the
 * record.
 *
 * Its limits are worth stating rather than implying. It cannot protect a first
 * contact: a directory that lies from the very beginning is believed. What it
 * does is turn the attack window from "every session, forever" into "one
 * moment" — a server that later starts handing out its own keys is caught
 * immediately, on every existing conversation at once, which is the realistic
 * shape of the threat.
 *
 * And a changed key is NOT automatically an attack. Reinstalls happen, devices
 * get wiped, people switch phones. So this reports and the human decides; it
 * does not block on its own. A port of `server/pkg/e2e/trust.go`.
 */
@Serializable
data class PinnedIdentity(
    val userId: String,
    val deviceId: String,
    /** base64 X25519 identity public key */
    val identityKey: String,
    /** base64 Ed25519 identity signing public key */
    val signingKey: String,
    val firstSeen: Long,
)

/** What a verification found. */
sealed interface TrustVerdict {
    /** Never seen before — pin after the session is established. */
    data object FirstUse : TrustVerdict

    /** The keys match what was pinned. */
    data object Known : TrustVerdict

    /**
     * The keys differ from the pin. The caller must NOT proceed silently: this
     * is the moment the safety number is worth showing.
     */
    data class Changed(val pinned: PinnedIdentity) : TrustVerdict
}

/**
 * The pin store.
 *
 * Pure state plus lookups, with persistence left to the caller — the same shape
 * as the ratchet session. That keeps this testable without a device and lets the
 * storage layer decide when a write is worth making.
 */
class TrustStore(pins: Map<String, PinnedIdentity> = emptyMap()) {
    private val pins = LinkedHashMap(pins)

    fun snapshot(): Map<String, PinnedIdentity> = LinkedHashMap(pins)

    fun verify(
        userId: String,
        deviceId: String,
        identityKey: String,
        signingKey: String,
    ): TrustVerdict {
        val pinned = pins[key(userId, deviceId)] ?: return TrustVerdict.FirstUse
        return if (equalKeys(pinned.identityKey, identityKey) &&
            equalKeys(pinned.signingKey, signingKey)
        ) {
            TrustVerdict.Known
        } else {
            TrustVerdict.Changed(pinned)
        }
    }

    /**
     * Records or replaces a pin. Replacing one is an explicit act: it should
     * follow a human confirming the change, not a client deciding on its own
     * that the new key is fine.
     */
    fun accept(userId: String, deviceId: String, identityKey: String, signingKey: String) {
        pins[key(userId, deviceId)] = PinnedIdentity(
            userId = userId,
            deviceId = deviceId,
            identityKey = identityKey,
            signingKey = signingKey,
            firstSeen = System.currentTimeMillis(),
        )
    }

    fun forget(userId: String, deviceId: String) {
        pins.remove(key(userId, deviceId))
    }

    private fun key(userId: String, deviceId: String) = "$userId:$deviceId"

    /**
     * Constant-time comparison of two base64 keys.
     *
     * These are public keys, so a timing leak is not a key compromise — but it
     * still tells an attacker which prefix of a forged key is correct, which is
     * a free hint to anyone grinding one. Anything that does not decode counts
     * as a mismatch: a malformed value must never read as a match.
     */
    private fun equalKeys(a: String, b: String): Boolean {
        val left = B64.decodeOrNull(a) ?: return false
        val right = B64.decodeOrNull(b) ?: return false
        return Crypto.constantTimeEquals(left, right)
    }
}
