package com.syncapp.messenger.domain

import com.syncapp.messenger.domain.model.ChatFlags
import com.syncapp.messenger.domain.model.ChatKind
import com.syncapp.messenger.domain.model.Entitlements
import com.syncapp.messenger.domain.model.PaymentMethod
import com.syncapp.messenger.domain.model.PlanOffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The model-level decisions behind the security and Premium screens.
 *
 * Every one of these has a wrong answer that looks plausible on screen: a price rendered
 * through a float, a mute that never expires, a tier that grants everything because the
 * server said nothing yet. None of them would fail a build.
 */
class ChatFlagsTest {

    @Test
    fun `a mute in the future is muted and one in the past is not`() {
        val now = 1_700_000_000_000
        // Derived from the deadline rather than stored as a boolean, so a mute expires on
        // its own with nothing having to run at the moment it does.
        assertTrue(ChatFlags(mutedUntil = now + 1).isMuted(now))
        assertFalse(ChatFlags(mutedUntil = now - 1).isMuted(now))
    }

    @Test
    fun `zero means not muted`() {
        // The unmute value on the wire. Treating it as "muted until 1970" would be
        // harmless in a comparison and wrong the moment it is shown.
        assertFalse(ChatFlags(mutedUntil = 0).isMuted(1_700_000_000_000))
    }

    @Test
    fun `the deadline boundary counts as unmuted`() {
        val now = 1_700_000_000_000
        assertFalse(ChatFlags(mutedUntil = now).isMuted(now))
    }
}

class ChatKindTest {

    @Test
    fun `secret is two-party like direct`() {
        // The check that decides whether a row is named after its peer, whether the
        // presence dot renders, and whether a preview gets a sender prefix. Writing it as
        // `kind == DIRECT` is why secret chats appeared with no name and no presence.
        assertTrue(ChatKind.SECRET.isTwoParty)
        assertTrue(ChatKind.DIRECT.isTwoParty)
        assertFalse(ChatKind.GROUP.isTwoParty)
        assertFalse(ChatKind.CHANNEL.isTwoParty)
    }

    @Test
    fun `only secret is end-to-end`() {
        assertTrue(ChatKind.SECRET.isEndToEnd)
        assertFalse(ChatKind.DIRECT.isEndToEnd)
        assertFalse(ChatKind.GROUP.isEndToEnd)
    }
}

class PlanOfferTest {

    private fun offer(amountMinor: Long) = PlanOffer(
        plan = "premium",
        amountMinor = amountMinor,
        currency = "RUB",
        periodDays = 30,
        methods = listOf(PaymentMethod.SBP),
    )

    @Test
    fun `a price with no minor part omits the decimals`() {
        assertEquals("299 RUB", offer(29_900).priceText())
    }

    @Test
    fun `minor units are zero-padded`() {
        // 9.05, not 9.5. Formatting the remainder without padding is the bug that turns
        // five kopeks into fifty.
        assertEquals("9.05 RUB", offer(905).priceText())
    }

    @Test
    fun `a price under one major unit still renders`() {
        assertEquals("0.99 RUB", offer(99).priceText())
    }

    @Test
    fun `the arithmetic is exact`() {
        // The reason the whole pipeline stays in minor units: 1999 / 100.0 formatted is
        // where a payment screen starts showing a number that differs from the charge.
        assertEquals("19.99 RUB", offer(1_999).priceText())
        assertEquals("0 RUB", offer(0).priceText())
    }
}

class PaymentMethodTest {

    @Test
    fun `known methods map from the wire`() {
        assertEquals(PaymentMethod.CARD, PaymentMethod.fromWire("card"))
        assertEquals(PaymentMethod.SBP, PaymentMethod.fromWire("sbp"))
    }

    @Test
    fun `an unknown method is null rather than a fallback`() {
        // Null so the caller DROPS it. A button labelled with a method this build cannot
        // start fails only after somebody has committed to paying, and the server will add
        // methods faster than the app ships.
        assertNull(PaymentMethod.fromWire("crypto"))
        assertNull(PaymentMethod.fromWire(""))
        assertNull(PaymentMethod.fromWire("CARD"))
    }
}

class EntitlementsTest {

    @Test
    fun `the pre-answer default grants nothing`() {
        // The safe direction: a feature shown as available and then refused is worse than
        // one that appears a moment late.
        val unknown = Entitlements.UNKNOWN
        assertFalse(unknown.isPremium)
        assertFalse(unknown.secretChats)
        assertFalse(unknown.folders)
        assertEquals(0L, unknown.maxUploadBytes)
    }

    @Test
    fun `premium is any plan that is not free`() {
        // Read as "not free" rather than compared to the literal "premium": the server
        // owns the plan vocabulary and may add tiers, and a client that hardcodes one name
        // treats every new tier as free.
        assertFalse(Entitlements(plan = "free").isPremium)
        assertFalse(Entitlements(plan = "").isPremium)
        assertTrue(Entitlements(plan = "premium").isPremium)
        assertTrue(Entitlements(plan = "premium_annual").isPremium)
    }

    @Test
    fun `entitlements are read from the flags and not from the plan`() {
        // The case that makes flag-based gating necessary: a deployment with no billing
        // service reports the plan as free AND grants everything. A client gating on the
        // name would hide every feature on exactly that installation.
        val ungated = Entitlements(plan = "free", secretChats = true, folders = true)
        assertFalse(ungated.isPremium)
        assertTrue(ungated.secretChats)
        assertTrue(ungated.folders)
    }
}
