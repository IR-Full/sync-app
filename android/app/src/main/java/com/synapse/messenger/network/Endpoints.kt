package com.synapse.messenger.network

/**
 * Where to reach the deployment. Resolved per call rather than injected as a
 * constant so a debug build can repoint the app at another gateway from the
 * settings screen without a restart (see `EnvironmentStore`).
 */
interface GatewayEndpointProvider {
    /**
     * WebSocket URL of the gateway, e.g. `wss://host/ws`.
     *
     * Only this one endpoint is configurable, because it is the only one a client
     * chooses: the media URLs are absolute and HMAC-signed by the gateway itself,
     * so there is no second host to point anywhere.
     */
    suspend fun gatewayUrl(): String
}

/**
 * The stable per-installation device id. The gateway keys sessions, push tokens
 * and multi-device delivery on it, so it must outlive a logout: rotating it would
 * orphan the device row that holds this phone's push token.
 */
interface DeviceIdProvider {
    suspend fun deviceId(): String
}
