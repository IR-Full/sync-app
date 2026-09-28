package com.syncapp.messenger.domain.model

/**
 * Whether a second factor is on, and how much recovery is left.
 *
 * [recoveryCodes] is non-empty on exactly one occasion — the reply to a confirmation.
 * The server stores argon2id hashes, so that is the only moment they exist in readable
 * form, and a screen that fails to show them there has silently removed the account's
 * only way back in.
 */
data class TwoFactorState(
    val enabled: Boolean = false,
    /**
     * Unused recovery codes remaining.
     *
     * Surfaced because losing the last code and the phone together is the state there
     * is no way back from, and a screen that cannot see the count cannot warn before it
     * happens.
     */
    val recoveryCodesLeft: Int = 0,
    val recoveryCodes: List<String> = emptyList(),
    val confirmedAtMs: Long = 0,
)

/** What enrolment hands back, before anything is enforced. */
data class TwoFactorSetup(
    /** Base32, for the people who type it in rather than scanning. */
    val secret: String,
    /** The `otpauth://` URI, for a QR code. */
    val uri: String,
)

/**
 * What this account's tier grants.
 *
 * Explicit flags rather than a plan name, deliberately. A client that switched on
 * `plan == "premium"` would hold a second copy of the entitlement policy and the two
 * would drift — and worse, a deployment with no billing service reports the plan as
 * `free` while granting everything, so name-based gating hides every feature on exactly
 * the installation where they are all available.
 */
data class Entitlements(
    val plan: String = "free",
    val status: String = "",
    /** When access lapses without a renewal, unix millis. 0 = no expiry. */
    val periodEndMs: Long = 0,
    val cancelAtPeriodEnd: Boolean = false,
    val secretChats: Boolean = false,
    val maxUploadBytes: Long = 0,
    val maxPinnedChats: Int = 0,
    val folders: Boolean = false,
    val advancedSearch: Boolean = false,
    val priorityDelivery: Boolean = false,
    val voiceTranscription: Boolean = false,
    val badge: Boolean = false,
) {
    val isPremium: Boolean get() = plan.isNotEmpty() && plan != "free"

    companion object {
        /**
         * What to draw before the server has answered.
         *
         * Everything OFF, which is the safe direction: a feature shown as available and
         * then refused is worse than one that appears a moment late.
         *
         * This is NOT the "billing not configured" case. `BILLING_STATUS` always
         * answers — a gateway with no billing service replies with everything granted
         * rather than erroring — so a deployment that sells nothing still sends real
         * entitlements on connect. This value is on screen only for the moment before
         * that reply lands.
         */
        val UNKNOWN = Entitlements()
    }
}

/**
 * How a payment is taken.
 *
 * The SERVER decides which are available, from the country. A client that picked its
 * own provider would be choosing which regulator applies to the transaction, so this is
 * a list of what was offered rather than of what the app knows how to draw.
 */
enum class PaymentMethod(val wire: String) {
    CARD("card"),
    SBP("sbp"),
    ;

    companion object {
        /**
         * Unknown methods map to null and are DROPPED by the caller, not surfaced as a
         * raw string: a button labelled with a method this build cannot start is worse
         * than no button, and the server will add methods faster than the app ships.
         */
        fun fromWire(value: String): PaymentMethod? =
            entries.firstOrNull { it.wire == value }
    }
}

/** One purchasable plan in this market. */
data class PlanOffer(
    val plan: String,
    /**
     * The price in the currency's MINOR unit — kopeks, cents.
     *
     * An integer, never a float. A price is an exact quantity and binary floating point
     * cannot hold 0.01, so the whole pipeline from the acquirer to this field stays in
     * minor units and only the formatting divides.
     */
    val amountMinor: Long,
    val currency: String,
    val periodDays: Int,
    val methods: List<PaymentMethod>,
) {
    /**
     * The price as text, by integer arithmetic.
     *
     * Never through `Double`: dividing by 100 and formatting is how a price becomes
     * 9.989999999999999, and a payment screen is the last place to explain floating
     * point to somebody.
     */
    fun priceText(): String {
        val major = amountMinor / 100
        val minor = amountMinor % 100
        val amount = if (minor == 0L) "$major" else "$major.${minor.toString().padStart(2, '0')}"
        return "$amount $currency"
    }
}

/** Where to send the user to finish paying. */
data class PaymentIntent(
    val paymentId: String,
    val status: String,
    /**
     * Followed in a browser. Empty for a method with no redirect.
     *
     * Not interchangeable with [qrPayload] — see that field.
     */
    val payUrl: String = "",
    /**
     * DISPLAYED as a QR code.
     *
     * An SBP payload is not a URL and must not be opened as one: doing so produces a
     * dead link, and rendering a redirect URL as a QR code produces a code that leads
     * to a web page instead of to a payment.
     */
    val qrPayload: String = "",
    /**
     * The server returned an EXISTING payment for this idempotency key rather than
     * creating a second one. Worth knowing: it means the button was tapped twice, not
     * that a second charge was made.
     */
    val deduplicated: Boolean = false,
)
