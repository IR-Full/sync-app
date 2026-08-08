package com.synapse.messenger.data.sync

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import androidx.core.content.getSystemService
import com.synapse.messenger.core.AppScope
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.stateIn

/**
 * Whether the device has usable connectivity.
 *
 * The gateway already retries with jittered backoff, so this is not the reconnect
 * mechanism — it is what collapses the wait. Coming back from a tunnel, the socket
 * may be mid-backoff for another twenty seconds; a network-available callback lets
 * the app reconnect immediately instead.
 */
@Singleton
class NetworkMonitor @Inject constructor(
    context: Context,
    @AppScope scope: CoroutineScope,
) {
    private val connectivityManager = context.getSystemService<ConnectivityManager>()

    val online: StateFlow<Boolean> = callbackFlow {
        val manager = connectivityManager
        if (manager == null) {
            // No ConnectivityManager: assume online rather than block every send.
            trySend(true)
            awaitClose { }
            return@callbackFlow
        }

        val callback = object : ConnectivityManager.NetworkCallback() {
            private val available = mutableSetOf<Network>()

            override fun onAvailable(network: Network) {
                available += network
                trySend(true)
            }

            override fun onLost(network: Network) {
                available -= network
                trySend(available.isNotEmpty())
            }
        }
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        manager.registerNetworkCallback(request, callback)
        trySend(manager.activeNetwork != null)
        awaitClose { manager.unregisterNetworkCallback(callback) }
    }
        .distinctUntilChanged()
        .stateIn(scope, SharingStarted.Eagerly, true)
}
