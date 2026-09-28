package com.syncapp.messenger.data.repository

import com.syncapp.messenger.core.AppScope
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.core.runOutcome
import com.syncapp.messenger.domain.model.Entitlements
import com.syncapp.messenger.domain.model.PaymentIntent
import com.syncapp.messenger.domain.model.PaymentMethod
import com.syncapp.messenger.domain.model.PlanOffer
import com.syncapp.messenger.domain.model.TwoFactorSetup
import com.syncapp.messenger.domain.model.TwoFactorState
import com.syncapp.messenger.domain.repository.AccountSecurityRepository
import com.syncapp.messenger.network.SyncAppGateway
import com.syncapp.messenger.network.protocol.BillingCheckout
import com.syncapp.messenger.network.protocol.BillingOffers
import com.syncapp.messenger.network.protocol.BillingPayment
import com.syncapp.messenger.network.protocol.BillingPlans
import com.syncapp.messenger.network.protocol.MsgType
import com.syncapp.messenger.network.protocol.PasswordChange
import com.syncapp.messenger.network.protocol.PasswordChanged
import com.syncapp.messenger.network.protocol.Subscription
import com.syncapp.messenger.network.protocol.TOTPConfirm
import com.syncapp.messenger.network.protocol.TOTPDisable
import com.syncapp.messenger.network.protocol.TOTPSetupInfo
import com.syncapp.messenger.network.protocol.TOTPState
import com.syncapp.messenger.network.request
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Password, second factor, and the Premium tier.
 *
 * Nothing is cached. All three are server-owned state with no offline meaning: a
 * password change has to reach the server to be a change at all, and an entitlement read
 * from a stale cache is how a client offers a feature the server has stopped honouring.
 *
 * The one piece of held state is [entitlements], and it is held because it also arrives
 * as a PUSH. A subscription changes without this client asking — a payment settles
 * minutes after the user closed the checkout page, a period lapses at four in the
 * morning — and a screen that read it once at launch goes on drawing features that are
 * already being refused.
 */
@Singleton
class AccountSecurityRepositoryImpl @Inject constructor(
    private val gateway: SyncAppGateway,
    @param:AppScope private val scope: CoroutineScope,
) : AccountSecurityRepository {

    private val _entitlements = MutableStateFlow(Entitlements.UNKNOWN)
    override val entitlements: StateFlow<Entitlements> = _entitlements.asStateFlow()

    init {
        // Subscribed for the process lifetime rather than alongside a request. The event
        // can arrive at any moment, and a collector attached only during the initial
        // fetch would miss every later change — which is the whole reason the server
        // pushes it.
        scope.launch {
            gateway.subscriptions.collect { body -> _entitlements.value = body.toDomain() }
        }
    }

    override suspend fun changePassword(current: String, new: String): Outcome<Int> = runOutcome {
        gateway.request<PasswordChanged>(
            MsgType.PASSWORD_CHANGE,
            PasswordChange(oldPassword = current, newPassword = new),
        ).sessionsRevoked
    }

    override suspend fun twoFactorState(): Outcome<TwoFactorState> = runOutcome {
        // TOTP_STATE answers as a QUESTION, not only as the reply to a write. It used to
        // be reply-only, which left a settings screen with no way to learn whether the
        // factor was on — so it drew "off" on every fresh launch, inviting a second
        // enrolment and reporting the refusal as an error.
        gateway.request<TOTPState>(MsgType.TOTP_STATE, null).toDomain()
    }

    override suspend fun beginTwoFactor(): Outcome<TwoFactorSetup> = runOutcome {
        val info = gateway.request<TOTPSetupInfo>(MsgType.TOTP_SETUP, null)
        TwoFactorSetup(secret = info.secret, uri = info.uri)
    }

    override suspend fun confirmTwoFactor(code: String): Outcome<TwoFactorState> = runOutcome {
        gateway.request<TOTPState>(MsgType.TOTP_CONFIRM, TOTPConfirm(code = code)).toDomain()
    }

    override suspend fun disableTwoFactor(
        password: String,
        code: String,
    ): Outcome<TwoFactorState> = runOutcome {
        gateway.request<TOTPState>(
            MsgType.TOTP_DISABLE,
            TOTPDisable(password = password, code = code),
        ).toDomain()
    }

    override suspend fun refreshEntitlements(): Outcome<Entitlements> = runOutcome {
        gateway.request<Subscription>(MsgType.BILLING_STATUS, null).toDomain().also {
            _entitlements.value = it
        }
    }

    override suspend fun plans(country: String): Outcome<List<PlanOffer>> = runOutcome {
        gateway.request<BillingOffers>(MsgType.BILLING_PLANS, BillingPlans(country = country))
            .offers
            .map { offer ->
                PlanOffer(
                    plan = offer.plan,
                    amountMinor = offer.amountMinor,
                    currency = offer.currency,
                    periodDays = offer.periodDays,
                    // Unknown methods are dropped rather than shown as raw strings: a
                    // button for a method this build cannot start fails only after the
                    // user has committed to paying.
                    methods = offer.methods.mapNotNull(PaymentMethod::fromWire),
                )
            }
    }

    override suspend fun checkout(
        plan: String,
        method: PaymentMethod,
        country: String,
        returnUrl: String,
    ): Outcome<PaymentIntent> = runOutcome {
        // The idempotency key is minted HERE and nowhere else.
        //
        // It is the only thing between a double-tapped button and a double charge: the
        // server holds a unique index on it, so a repeat returns the first payment
        // instead of creating a second. A key the server invented would differ on every
        // retry, which is the same as having none.
        val payment = gateway.request<BillingPayment>(
            MsgType.BILLING_CHECKOUT,
            BillingCheckout(
                plan = plan,
                method = method.wire,
                idempotencyKey = UUID.randomUUID().toString(),
                country = country,
                returnUrl = returnUrl,
            ),
        )
        PaymentIntent(
            paymentId = payment.paymentId,
            status = payment.status,
            payUrl = payment.payUrl,
            qrPayload = payment.qrPayload,
            deduplicated = payment.deduplicated,
        )
    }

    override suspend fun cancelSubscription(): Outcome<Entitlements> = runOutcome {
        gateway.request<Subscription>(MsgType.BILLING_CANCEL, null).toDomain().also {
            _entitlements.value = it
        }
    }

    private fun TOTPState.toDomain() = TwoFactorState(
        enabled = enabled,
        recoveryCodesLeft = recoveryLeft,
        recoveryCodes = recoveryCodes,
        confirmedAtMs = confirmedAtMs,
    )

    private fun Subscription.toDomain() = Entitlements(
        plan = plan,
        status = status,
        periodEndMs = periodEnd,
        cancelAtPeriodEnd = cancelAtPeriodEnd,
        secretChats = secretChats,
        maxUploadBytes = maxUploadBytes,
        maxPinnedChats = maxPinnedChats,
        folders = folders,
        advancedSearch = advancedSearch,
        priorityDelivery = priorityDelivery,
        voiceTranscription = voiceTranscription,
        badge = badge,
    )
}
