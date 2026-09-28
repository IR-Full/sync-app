package com.syncapp.messenger.network

import okhttp3.CertificatePinner

/**
 * TLS certificate pinning for the gateway.
 *
 * Why this exists at all: this app already pins the KEYS OF THE PERSON it is talking
 * to — trust-on-first-use plus safety numbers, in `crypto/Trust.kt` — and did not pin
 * the server. That is out of proportion. An attacker with a certificate from any CA
 * the device trusts (a corporate MDM profile, a compromised authority) reads the
 * entire cloud side of the product: every ordinary chat, every profile, every session
 * token. End-to-end encryption is untouched by that attack and also does not help
 * with it, because it protects a mode most conversations are not in.
 *
 * Three decisions in here matter more than the mechanism:
 *
 * **SPKI hashes, not certificates.** A certificate pin breaks on every renewal, which
 * is every ninety days with an automated issuer — so it gets removed after the first
 * outage. A subject-public-key-info pin survives renewal as long as the key is reused,
 * which is the normal case.
 *
 * **Two pins, always.** One pin is a single point of failure that bricks every
 * installed copy of the app if the key is ever lost or has to be rotated in a hurry.
 * The second is a BACKUP key, kept offline and never served — it exists precisely so
 * that a rotation is a configuration change rather than a forced app update. A
 * deployment that supplies only one is refused for that reason.
 *
 * **An expiry.** A pin set with no end date is a pin nobody revisits, and the first
 * time it is examined is during the outage it caused. Past the date the pin stops
 * being enforced rather than starting to fail closed: a stale pin that blocks every
 * connection turns a forgotten config entry into a dead app, and the honest tradeoff
 * for an abandoned build is to fall back to ordinary CA validation.
 */
object CertificatePinning {

    /**
     * Builds the pinner for a host, or null when pinning is not configured.
     *
     * Null means "use ordinary CA validation", and that is the right default for a
     * self-hosted deployment: an operator running their own gateway with their own
     * certificate has no way to know our pins, and a client that refused to connect
     * without them would simply not work anywhere but our own servers.
     */
    fun pinnerFor(config: PinningConfig?, nowMs: Long = System.currentTimeMillis()): CertificatePinner? {
        if (config == null) return null
        if (config.host.isBlank()) return null
        // Fewer than two is refused, not accepted-with-a-warning. A single pin is worse
        // than none: it looks like protection and its failure mode is an app that
        // cannot connect at all, with no way to fix it short of shipping a release.
        if (config.spkiSha256.size < 2) return null
        if (config.expiresAtMs in 1..nowMs) return null

        val builder = CertificatePinner.Builder()
        for (hash in config.spkiSha256) {
            val trimmed = hash.trim()
            if (trimmed.isEmpty()) continue
            // OkHttp wants the "sha256/BASE64" form. Accepting both spellings means a
            // config copied from a `openssl` one-liner works as-is, and one copied from
            // another pinning tool does too.
            builder.add(config.host, if (trimmed.startsWith("sha256/")) trimmed else "sha256/$trimmed")
        }
        return builder.build()
    }

    /**
     * Extracts the host to pin from a gateway URL.
     *
     * A URL rather than a bare host is what the app already has, and parsing it here
     * means the pin cannot end up attached to the wrong name: a pin for "example.com"
     * on a client that connects to "gw.example.com" silently does nothing, because
     * OkHttp matches pins by hostname.
     */
    fun hostOf(gatewayUrl: String): String =
        runCatching {
            java.net.URI(gatewayUrl).host ?: ""
        }.getOrDefault("")
}

/**
 * The pins for one host.
 *
 * Deliberately a value type with no defaults for the hashes: there is no sensible
 * placeholder for a public key, and a config object that could be constructed empty
 * is one that eventually is.
 */
data class PinningConfig(
    val host: String,
    /**
     * Base64 SHA-256 of the subject public key info, with or without the `sha256/`
     * prefix. At least two: the certificate in use and an offline BACKUP, so a
     * rotation does not require an app update.
     */
    val spkiSha256: List<String>,
    /**
     * When the pin stops being enforced, in unix millis. 0 means no expiry.
     *
     * Past it the app falls back to ordinary CA validation rather than failing closed.
     * That is the deliberately weaker choice: a stale pin that blocks everything turns
     * a forgotten configuration entry into an app that cannot be used at all, and for
     * a build nobody is maintaining, CA validation is a great deal better than nothing.
     */
    val expiresAtMs: Long = 0,
)
