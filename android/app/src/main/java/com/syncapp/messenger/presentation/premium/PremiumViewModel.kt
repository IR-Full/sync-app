package com.syncapp.messenger.presentation.premium

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.syncapp.messenger.core.AppError
import com.syncapp.messenger.core.Outcome
import com.syncapp.messenger.domain.model.Entitlements
import com.syncapp.messenger.domain.model.PaymentIntent
import com.syncapp.messenger.domain.model.PaymentMethod
import com.syncapp.messenger.domain.model.PlanOffer
import com.syncapp.messenger.domain.repository.AccountSecurityRepository
import com.syncapp.messenger.network.protocol.ErrorCode
import dagger.hilt.android.lifecycle.HiltViewModel
import java.util.Locale
import javax.inject.Inject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class PremiumState(
    val entitlements: Entitlements = Entitlements.UNKNOWN,
    /**
     * Null while loading; EMPTY means payments are not configured on this server.
     *
     * The distinction matters: an empty list is a real answer, not a failure. A gateway
     * with no payment provider grants the features outright, so the screen says so
     * instead of showing an error.
     */
    val offers: List<PlanOffer>? = null,
    val paymentsUnavailable: Boolean = false,
    val busy: Boolean = false,
    val intent: PaymentIntent? = null,
    val error: AppError? = null,
)

/**
 * The Premium screen: what the tier grants, what it costs, and how to pay.
 */
@HiltViewModel
class PremiumViewModel @Inject constructor(
    private val security: AccountSecurityRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(PremiumState())
    val state: StateFlow<PremiumState> = _state.asStateFlow()

    /**
     * The country the plans are priced for.
     *
     * From the device locale rather than asked, because it selects the PROVIDER as well
     * as the currency — SBP in Russia, cards elsewhere — and letting somebody choose
     * would let them choose which regulator applies to their transaction. The server
     * treats it as a hint and decides for itself.
     */
    private val country: String = Locale.getDefault().country

    init {
        viewModelScope.launch {
            security.entitlements.collect { current ->
                _state.update { it.copy(entitlements = current) }
            }
        }
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            // Both, and in this order. The entitlements decide what the top of the screen
            // says; the offers decide whether there is anything to buy. A failure of the
            // second must not hide the first.
            security.refreshEntitlements()

            when (val result = security.plans(country)) {
                is Outcome.Success -> _state.update {
                    it.copy(offers = result.value, paymentsUnavailable = result.value.isEmpty())
                }
                is Outcome.Failure -> {
                    val unsupported = result.error is AppError.Rejected &&
                        (result.error as AppError.Rejected).code == ErrorCode.UNSUPPORTED
                    _state.update {
                        if (unsupported) {
                            it.copy(offers = emptyList(), paymentsUnavailable = true)
                        } else {
                            it.copy(offers = emptyList(), error = result.error)
                        }
                    }
                }
            }
        }
    }

    fun checkout(plan: String, method: PaymentMethod) {
        viewModelScope.launch {
            _state.update { it.copy(busy = true, error = null) }
            when (val result = security.checkout(plan, method, country)) {
                is Outcome.Success -> _state.update { it.copy(busy = false, intent = result.value) }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = result.error) }
            }
        }
    }

    fun cancel() {
        viewModelScope.launch {
            _state.update { it.copy(busy = true, error = null) }
            when (val result = security.cancelSubscription()) {
                // Cancelling does not withdraw access — the paid period runs out first —
                // so nothing here hides a feature.
                is Outcome.Success -> _state.update {
                    it.copy(busy = false, entitlements = result.value)
                }
                is Outcome.Failure -> _state.update { it.copy(busy = false, error = result.error) }
            }
        }
    }

    fun dismissIntent() {
        _state.update { it.copy(intent = null) }
    }

    fun clearError() {
        _state.update { it.copy(error = null) }
    }
}
