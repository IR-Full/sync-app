package com.synapse.messenger.network

import android.util.Log
import com.synapse.messenger.core.AppScope
import com.synapse.messenger.network.protocol.Auth
import com.synapse.messenger.network.protocol.AuthOk
import com.synapse.messenger.network.protocol.BodyCodec
import com.synapse.messenger.network.protocol.Cap
import com.synapse.messenger.network.protocol.ChatInfo
import com.synapse.messenger.network.protocol.Envelope
import com.synapse.messenger.network.protocol.ErrorCode
import com.synapse.messenger.network.protocol.Frame
import com.synapse.messenger.network.protocol.Hello
import com.synapse.messenger.network.protocol.MsgType
import com.synapse.messenger.network.protocol.NewMessage
import com.synapse.messenger.network.protocol.Presence
import com.synapse.messenger.network.protocol.ProtocolError
import com.synapse.messenger.network.protocol.ProtocolException
import com.synapse.messenger.network.protocol.ReadUpdate
import com.synapse.messenger.network.protocol.Resume
import com.synapse.messenger.network.protocol.ResumeOk
import com.synapse.messenger.network.protocol.SendAck
import com.synapse.messenger.network.protocol.Typing
import com.synapse.messenger.network.protocol.Welcome
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.locks.ReentrantLock
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.concurrent.withLock
import kotlin.math.min
import kotlin.random.Random
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString

/**
 * The whole custom-protocol surface, in one place.
 *
 * The gateway speaks a binary protocol over a WebSocket (`/ws`), not REST: one
 * binary WS message == one frame == one envelope. Auth, sending, history,
 * receipts and typing all ride this single connection, so this class owns the
 * lifecycle end to end:
 *
 * ```
 *   dial -> HELLO/WELCOME (capability negotiation)
 *        -> AUTH or RESUME (identity)
 *        -> steady state: request/reply by requestId + unsolicited pushes
 *        -> drop -> jittered backoff -> redial, resuming when the server allows
 * ```
 *
 * It is deliberately transport-only: it knows nothing about Room, chats or
 * ViewModels. Everything above subscribes to [events], which keeps the protocol
 * quirks (seq/ack bookkeeping, the streamed-history convention, request
 * correlation) from leaking upward.
 */
@Singleton
class SynapseGateway @Inject constructor(
    private val httpClient: OkHttpClient,
    private val endpoints: GatewayEndpointProvider,
    private val deviceIds: DeviceIdProvider,
    @param:AppScope private val scope: CoroutineScope,
) {
    private val _state = MutableStateFlow(ConnectionState.IDLE)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    /**
     * Server pushes, in arrival order. Unlimited capacity on purpose: the frames
     * are bounded by the gateway's own inflight window, and a dropped NEW is a
     * message the user never sees.
     */
    private val eventChannel = Channel<ServerEvent>(Channel.UNLIMITED)
    val events: Flow<ServerEvent> = eventChannel.receiveAsFlow()

    /** Our per-connection monotonic sequence; restarts at 1 on every socket. */
    private val seq = AtomicLong(0)

    /** Highest server seq observed — piggybacked as `ack` and replayed from on RESUME. */
    private val ackSeq = AtomicLong(0)
    private val requestCounter = AtomicLong(0)
    private val pending = ConcurrentHashMap<Long, PendingRequest>()

    /** Serialises seq assignment with the actual send so the wire stays ordered. */
    private val writeLock = ReentrantLock()

    @Volatile private var socket: WebSocket? = null

    @Volatile private var credentials: Credentials? = null

    @Volatile private var session: GatewaySession? = null

    @Volatile private var deviceId: String = ""

    @Volatile private var negotiatedCaps: Int = 0

    @Volatile private var heartbeatMs: Int = DEFAULT_HEARTBEAT_MS

    @Volatile private var lastFrameAt: Long = 0

    @Volatile private var closedSignal: CompletableDeferred<Unit>? = null

    private var connectionJob: Job? = null
    private var attempt = 0

    val currentSession: GatewaySession? get() = session

    /**
     * Starts (or replaces) the connection loop and suspends until the first
     * successful authentication, so a caller can await "usable", not merely
     * "socket open". Later drops are retried in the background.
     */
    suspend fun connect(credentials: Credentials): GatewaySession {
        disconnect(clearSession = false)
        this.credentials = credentials
        val firstSession = CompletableDeferred<GatewaySession>()
        connectionJob = scope.launch {
            attempt = 0
            while (isActive) {
                val outcome = runCatching { runConnection(firstSession) }
                val error = outcome.exceptionOrNull()
                if (error is CancellationException) throw error

                if (error != null) Log.i(TAG, "connection ended: ${error.message}")
                failAllPending(error ?: ConnectionClosedException())

                // An auth-class rejection cannot be fixed by retrying: the app has
                // to send the user back to login.
                if (error is ProtocolException && error.isAuthFailure) {
                    _state.value = ConnectionState.CLOSED
                    if (!firstSession.isCompleted) firstSession.completeExceptionally(error)
                    eventChannel.trySend(ServerEvent.SessionExpired(error))
                    return@launch
                }
                // The very first attempt fails loudly — someone is awaiting it.
                if (!firstSession.isCompleted && session == null) {
                    _state.value = ConnectionState.CLOSED
                    firstSession.completeExceptionally(error ?: ConnectionClosedException())
                    return@launch
                }

                _state.value = ConnectionState.RECONNECTING
                delay(backoffDelay(attempt++))
            }
        }
        return firstSession.await()
    }

    /** Closes for good: no reconnect, every in-flight request rejected. */
    fun disconnect(clearSession: Boolean = true) {
        connectionJob?.cancel()
        connectionJob = null
        socket?.cancel()
        socket = null
        closedSignal?.complete(Unit)
        failAllPending(ConnectionClosedException())
        credentials = null
        if (clearSession) session = null
        _state.value = ConnectionState.CLOSED
    }

    // ---------------------------------------------------------------- one socket

    private suspend fun runConnection(firstSession: CompletableDeferred<GatewaySession>) {
        _state.value = if (attempt > 0) ConnectionState.RECONNECTING else ConnectionState.CONNECTING
        seq.set(0)
        if (deviceId.isEmpty()) deviceId = deviceIds.deviceId()

        val url = endpoints.gatewayUrl()
        val opened = CompletableDeferred<Unit>()
        val closed = CompletableDeferred<Unit>()
        closedSignal = closed

        val ws = httpClient.newWebSocket(
            Request.Builder().url(url).build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    opened.complete(Unit)
                }

                override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                    handleFrame(bytes.toByteArray())
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    if (!opened.isCompleted) opened.completeExceptionally(t)
                    closed.complete(Unit)
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(NORMAL_CLOSURE, null)
                    closed.complete(Unit)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    closed.complete(Unit)
                }
            },
        )
        socket = ws
        lastFrameAt = System.currentTimeMillis()

        try {
            withTimeout(CONNECT_TIMEOUT_MS) { opened.await() }
            handshakeAndAuth(firstSession)
            attempt = 0
            // The gateway pings on the negotiated heartbeat; prolonged silence means
            // the socket is dead in a way the OS has not reported yet (suspended
            // phone, flaky cell handover). Force the cycle instead of waiting.
            val watchdog = scope.launch {
                while (isActive) {
                    delay(heartbeatMs.toLong())
                    if (System.currentTimeMillis() - lastFrameAt > heartbeatMs * LIVENESS_FACTOR) {
                        Log.i(TAG, "no frame for ${heartbeatMs * LIVENESS_FACTOR}ms; recycling socket")
                        ws.cancel()
                        return@launch
                    }
                }
            }
            try {
                closed.await()
            } finally {
                watchdog.cancel()
            }
        } finally {
            ws.cancel()
            if (socket === ws) socket = null
        }
    }

    private suspend fun handshakeAndAuth(firstSession: CompletableDeferred<GatewaySession>) {
        _state.value = ConnectionState.AUTHENTICATING

        val welcome: Welcome = request(
            MsgType.HELLO,
            Hello(
                clientVersion = CLIENT_VERSION,
                deviceId = deviceId,
                platform = PLATFORM,
                caps = Cap.CLIENT_CAPS,
            ),
        )
        negotiatedCaps = welcome.caps
        if (welcome.heartbeatMs > 0) heartbeatMs = welcome.heartbeatMs

        val existing = session
        val canResume = existing != null &&
            existing.resumeToken.isNotEmpty() &&
            welcome.resumeSupported &&
            Cap.has(negotiatedCaps, Cap.RESUME)

        if (canResume) {
            val resumed = runCatching {
                request<ResumeOk>(
                    MsgType.RESUME,
                    Resume(resumeToken = existing!!.resumeToken, lastAckSeq = ackSeq.get()),
                )
            }
            val error = resumed.exceptionOrNull()
            when {
                error == null -> {
                    onAuthenticated(existing!!, firstSession)
                    return
                }
                // A refused resume (expired replay buffer, revoked session) is
                // recoverable: the socket stays open and the gateway now waits for a
                // fresh AUTH, which we still hold a bearer token for.
                error is ProtocolException && error.isAuthFailure -> throw error
                error is CancellationException -> throw error
                else -> Log.i(TAG, "resume refused (${error.message}); falling back to AUTH")
            }
        }

        val creds = credentials ?: throw IllegalStateException("no credentials")
        val body = when (creds) {
            is Credentials.Token -> Auth(token = creds.token)
            is Credentials.Password -> Auth(
                username = creds.username,
                password = creds.password,
                register = creds.register,
            )
        }
        val ok: AuthOk = request(MsgType.AUTH, body)
        val fresh = GatewaySession(
            userId = ok.userId,
            deviceId = ok.deviceId,
            sessionId = ok.sessionId,
            token = ok.token,
            resumeToken = ok.resumeToken,
        )
        // The gateway assigns a device id when we sent none; adopt it so the next
        // reconnect presents the same device (multi-device delivery keys on it).
        if (fresh.deviceId.isNotEmpty()) deviceId = fresh.deviceId
        onAuthenticated(fresh, firstSession)
    }

    private fun onAuthenticated(
        newSession: GatewaySession,
        firstSession: CompletableDeferred<GatewaySession>,
    ) {
        session = newSession
        // Once authenticated a token beats a password for any future reconnect: it
        // survives a password change and keeps credentials out of memory.
        if (newSession.token.isNotEmpty()) credentials = Credentials.Token(newSession.token)
        _state.value = ConnectionState.READY
        eventChannel.trySend(ServerEvent.Authenticated(newSession))
        if (!firstSession.isCompleted) firstSession.complete(newSession)
    }

    /** Exponential backoff with full jitter — spreads a fleet-wide reconnect storm. */
    private fun backoffDelay(attempt: Int): Long {
        val base = min(BASE_BACKOFF_MS shl min(attempt, 5), MAX_BACKOFF_MS)
        return base / 2 + Random.nextLong(base / 2 + 1)
    }

    // ------------------------------------------------------------------- inbound

    private fun handleFrame(raw: ByteArray) {
        lastFrameAt = System.currentTimeMillis()
        val envelope = try {
            Envelope.decode(Frame.decode(raw))
        } catch (e: Exception) {
            Log.w(TAG, "dropping malformed frame", e)
            return
        }

        // Track the highest server sequence: it is both the piggybacked ack and the
        // replay cursor a RESUME starts from.
        while (true) {
            val current = ackSeq.get()
            if (envelope.seq <= current || ackSeq.compareAndSet(current, envelope.seq)) break
        }

        when (envelope.type) {
            MsgType.PING -> {
                // The gateway tears down a connection that stops answering, and this
                // is the answer.
                runCatching { write(MsgType.PONG, null, envelope.requestId) }
                return
            }
            MsgType.PONG, MsgType.T_ACK -> return
        }

        val body = try {
            BodyCodec.decode(envelope.type, envelope.body)
        } catch (e: Exception) {
            Log.w(TAG, "cannot decode ${MsgType.name(envelope.type)} body", e)
            return
        }

        val waiting = pending[envelope.requestId]
        if (envelope.requestId != 0L && waiting != null) {
            settle(waiting, envelope.type, body)
            return
        }
        dispatchPush(envelope.type, body)
    }

    private fun settle(request: PendingRequest, type: Int, body: Any?) {
        if (type == MsgType.ERROR || type == MsgType.AUTH_ERR) {
            val error = body as? ProtocolError
            pending.remove(request.id)
            request.result.completeExceptionally(
                ProtocolException(
                    code = error?.code ?: ErrorCode.INTERNAL,
                    message = error?.message?.takeIf { it.isNotEmpty() } ?: "unknown error",
                    retryAfterMs = error?.retryAfterMs ?: 0,
                ),
            )
            return
        }

        // Paged replies (HISTORY, and the same convention for THREAD/CHAT_EXPORT)
        // stream N item frames sharing our requestId, then a terminator carrying the
        // cursor. Keep the request open until the terminator lands.
        val stream = request.stream
        if (stream != null && type == stream.itemType) {
            if (body != null) stream.items += body
            return
        }

        pending.remove(request.id)
        request.result.complete(ReplyEnvelope(type, body))
    }

    private fun dispatchPush(type: Int, body: Any?) {
        val event = when (type) {
            MsgType.NEW -> (body as? NewMessage)?.let(ServerEvent::Message)
            MsgType.SEND_ACK -> (body as? SendAck)?.let(ServerEvent::Acked)
            MsgType.READ_UPD -> (body as? ReadUpdate)?.let(ServerEvent::ReadReceipt)
            MsgType.TYPING -> (body as? Typing)?.let(ServerEvent::TypingSignal)
            MsgType.PRESENCE -> (body as? Presence)?.let(ServerEvent::PresenceUpdate)
            MsgType.CHAT_INFO -> (body as? ChatInfo)?.let(ServerEvent::ChatCreated)
            MsgType.ERROR, MsgType.AUTH_ERR -> {
                val raw = body as? ProtocolError
                val error = ProtocolException(
                    code = raw?.code ?: ErrorCode.INTERNAL,
                    message = raw?.message?.takeIf { it.isNotEmpty() } ?: "unknown error",
                    retryAfterMs = raw?.retryAfterMs ?: 0,
                )
                if (error.isAuthFailure) ServerEvent.SessionExpired(error) else ServerEvent.Failure(error)
            }
            // Unknown types are skipped by design: that is the protocol's
            // forward-compatibility rule, not an oversight.
            else -> null
        }
        if (event != null) eventChannel.trySend(event)
    }

    private fun failAllPending(cause: Throwable) {
        val snapshot = pending.values.toList()
        pending.clear()
        snapshot.forEach { it.result.completeExceptionally(cause) }
    }

    // ------------------------------------------------------------------ outbound

    private fun write(type: Int, body: Any?, requestId: Long) {
        val ws = socket ?: throw ConnectionClosedException()
        val payload = BodyCodec.encode(type, body)
        writeLock.withLock {
            val envelope = Envelope(
                type = type,
                seq = seq.incrementAndGet(),
                ack = ackSeq.get(),
                requestId = requestId,
                body = payload,
            )
            val sent = ws.send(Frame.encode(envelope.encode()).toByteString())
            if (!sent) throw ConnectionClosedException()
        }
    }

    /**
     * Fire-and-forget. Used for the types the gateway answers only on failure:
     * READ, TYPING, EDIT, DELETE. A silent success is the protocol's design, not
     * a missing reply.
     */
    fun send(type: Int, body: Any?) {
        requireReady()
        write(type, body, 0)
    }

    /**
     * Sends a request and waits for its correlated reply. ERROR frames throw
     * [ProtocolException]; any other correlated type resolves, so a caller that
     * accepts several reply shapes can inspect [ReplyEnvelope.type].
     */
    suspend fun requestEnvelope(
        type: Int,
        body: Any?,
        timeoutMs: Long = REQUEST_TIMEOUT_MS,
        skipReadyCheck: Boolean = false,
    ): ReplyEnvelope {
        if (!skipReadyCheck) requireReady()
        val id = requestCounter.incrementAndGet()
        val request = PendingRequest(id)
        pending[id] = request
        return try {
            write(type, body, id)
            withTimeout(timeoutMs) { request.result.await() }
        } catch (e: TimeoutCancellationException) {
            throw RequestTimeoutException(type)
        } finally {
            pending.remove(id)
        }
    }

    /** [requestEnvelope] with the reply body cast to the expected shape. */
    suspend inline fun <reified T> request(
        type: Int,
        body: Any?,
        timeoutMs: Long = REQUEST_TIMEOUT_MS,
    ): T {
        // The handshake runs before the connection is READY by definition, so those
        // three types are exempt from the readiness gate.
        val handshake = type == MsgType.HELLO || type == MsgType.AUTH || type == MsgType.RESUME
        val reply = requestEnvelope(type, body, timeoutMs, skipReadyCheck = handshake)
        return reply.body as? T
            ?: throw UnexpectedReplyException(type, reply.type)
    }

    /**
     * Sends a request whose reply is a page: N frames of [itemType] followed by a
     * terminator of [endType], all sharing our requestId.
     *
     * This is how HISTORY works — the gateway replays stored messages as ordinary
     * NEW frames so a client's normal ingest path handles them, then closes the
     * page with HISTORY_OK carrying the next cursor.
     */
    suspend fun requestStream(
        type: Int,
        body: Any?,
        itemType: Int,
        timeoutMs: Long = REQUEST_TIMEOUT_MS,
    ): StreamReply {
        requireReady()
        val id = requestCounter.incrementAndGet()
        val request = PendingRequest(id, StreamState(itemType))
        pending[id] = request
        return try {
            write(type, body, id)
            val end = withTimeout(timeoutMs) { request.result.await() }
            StreamReply(items = request.stream!!.items.toList(), end = end.body)
        } catch (e: TimeoutCancellationException) {
            throw RequestTimeoutException(type)
        } finally {
            pending.remove(id)
        }
    }

    private fun requireReady() {
        if (_state.value != ConnectionState.READY) throw NotConnectedException(_state.value)
    }

    private class PendingRequest(val id: Long, val stream: StreamState? = null) {
        val result = CompletableDeferred<ReplyEnvelope>()
    }

    private class StreamState(val itemType: Int) {
        val items = mutableListOf<Any>()
    }

    companion object {
        private const val TAG = "SynapseGateway"
        private const val CLIENT_VERSION = "android/0.1"
        private const val PLATFORM = "android"
        private const val NORMAL_CLOSURE = 1000
        private const val DEFAULT_HEARTBEAT_MS = 20_000
        private const val LIVENESS_FACTOR = 3
        private const val CONNECT_TIMEOUT_MS = 20_000L
        const val REQUEST_TIMEOUT_MS = 20_000L
        private const val BASE_BACKOFF_MS = 1_000L
        private const val MAX_BACKOFF_MS = 30_000L
    }
}

/** A correlated reply: its type plus the decoded body (null for bodiless frames). */
data class ReplyEnvelope(val type: Int, val body: Any?)

/** A streamed page: the item frames plus the terminator body carrying the cursor. */
data class StreamReply(val items: List<Any>, val end: Any?)

class ConnectionClosedException : Exception("gateway connection closed")

class NotConnectedException(state: ConnectionState) :
    Exception("gateway not ready (state: $state)")

class RequestTimeoutException(type: Int) :
    Exception("request ${MsgType.name(type)} timed out")

class UnexpectedReplyException(requested: Int, received: Int) :
    Exception("${MsgType.name(requested)} answered with ${MsgType.name(received)}")
