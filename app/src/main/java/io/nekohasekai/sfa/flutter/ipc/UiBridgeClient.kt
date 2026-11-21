package io.nekohasekai.sfa.flutter.ipc

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.ServiceConnection
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.util.Log
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.StandardMethodCodec
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.Closeable
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/** The UI process forwards only application channels; Flutter's embedding channels stay local. */
internal class UiBridgeClient(
    private val context: Context,
    private val engineMessenger: BinaryMessenger,
    private val uiHandler: suspend (String, Any?) -> Any?,
    private val uiScope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate),
) : Closeable {
    private val transportJob = SupervisorJob()
    private val scope = CoroutineScope(transportJob + Dispatchers.Main.immediate)
    private val closed = AtomicBoolean()
    private var bound = false
    @Volatile private var session: IBoxUiSession? = null
    private var sessionReady = CompletableDeferred<IBoxUiSession>()
    private var reconnectJob: Job? = null
    private var reconnectAttempts = 0
    @Volatile private var generation = 0L
    private var everConnected = false
    private var desiredForeground = false
    private val rememberedControls = mutableMapOf<String, Any?>()
    private var eventListen: ByteArray? = null
    private val nextRequest = AtomicLong()
    private val replies = ConcurrentHashMap<Long, (ByteArray?) -> Unit>()
    private val uiJobs = ConcurrentHashMap.newKeySet<Job>()
    private val _connected = MutableStateFlow(false)
    val connected = _connected.asStateFlow()

    private enum class Kind { MESSAGE, REPLY, UI_REQUEST, CLOSED }
    private data class Incoming(
        val kind: Kind,
        val channel: String?,
        val descriptor: ParcelFileDescriptor?,
        val id: Long,
        val generation: Long,
        val reason: String? = null,
        val recoverable: Boolean = false,
    )

    private val incoming = Channel<Incoming>(Channel.UNLIMITED)
    private val sender = IpcSender(context, scope) { error ->
        scope.launch { reconnect(error) }
    }
    private fun callbackFor(expectedGeneration: Long) = object : IBoxUiCallback.Stub() {
        override fun onMessage(channel: String, message: ParcelFileDescriptor?, requestId: Long) {
            receive(Kind.MESSAGE, channel, message, requestId, expectedGeneration)
        }

        override fun onReply(requestId: Long, message: ParcelFileDescriptor?) {
            receive(Kind.REPLY, null, message, requestId, expectedGeneration)
        }

        override fun onUiRequest(requestId: Long, message: ParcelFileDescriptor?) {
            receive(Kind.UI_REQUEST, null, message, requestId, expectedGeneration)
        }

        override fun onClosed(reason: String, recoverable: Boolean) {
            if (expectedGeneration == generation) {
                incoming.trySend(Incoming(Kind.CLOSED, null, null, 0L, expectedGeneration, reason, recoverable))
            }
        }
    }

    private val connection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName, service: IBinder) {
            reconnectJob?.cancel()
            reconnectJob = null
            val expectedGeneration = ++generation
            // A previous failed/closed readiness future cannot accept a new Binder.
            if (sessionReady.isCompleted) sessionReady = CompletableDeferred()
            val ready = sessionReady
            val callback = callbackFor(expectedGeneration)
            scope.launch {
                try {
                    val remote = withContext(Dispatchers.IO) {
                        IBoxUiService.Stub.asInterface(service).openSession(callback)
                    }
                    if (closed.get() || expectedGeneration != generation) {
                        runCatching { remote.close() }
                        return@launch
                    }
                    session = remote
                    ready.complete(remote)
                    _connected.value = true
                    restoreSession(everConnected)
                    everConnected = true
                } catch (error: Exception) {
                    if (expectedGeneration == generation) {
                        if (error is SecurityException) terminate(error) else reconnect(error)
                    }
                }
            }
        }

        override fun onServiceDisconnected(name: ComponentName) {
            // Android can restore this binding itself; rebind if it has not done so shortly.
            reconnect(IllegalStateException("Native UI backend disconnected"), waitForSystem = true)
        }

        override fun onBindingDied(name: ComponentName) {
            reconnect(IllegalStateException("Native UI backend binding died"))
        }

        override fun onNullBinding(name: ComponentName) {
            terminate(IllegalStateException("Native UI backend is unavailable"))
        }
    }

    init {
        FLUTTER_CHANNELS.forEach { channel ->
            engineMessenger.setMessageHandler(channel) { message, reply ->
                if (closed.get()) {
                    reply.reply(IpcPayload.writtenBuffer(IpcPayload.errorBytes(IllegalStateException("UI bridge is closed"))))
                } else {
                    val bytes = IpcPayload.readableBytes(message)
                    if (channel == EVENTS_CHANNEL && bytes != null) rememberEventSubscription(bytes)
                    submit(channel, bytes) { bytes ->
                        reply.reply(IpcPayload.writtenBuffer(bytes))
                    }
                }
            }
        }
        scope.launch(Dispatchers.IO) {
            try {
                for (message in incoming) {
                    if (closed.get() || message.generation != generation) {
                        closeDescriptor(message.descriptor)
                        continue
                    }
                    try {
                        val bytes = IpcPayload.read(message.descriptor)
                        withContext(Dispatchers.Main.immediate) {
                            if (message.kind == Kind.MESSAGE || message.kind == Kind.UI_REQUEST) sessionReady.await()
                            if (!closed.get() && message.generation == generation) dispatch(message, bytes)
                        }
                    } catch (error: Exception) {
                        closeDescriptor(message.descriptor)
                        withContext(Dispatchers.Main.immediate) {
                            if (message.generation == generation) reconnect(error)
                        }
                    }
                }
            } finally {
                while (true) {
                    val pending = incoming.tryReceive().getOrNull() ?: break
                    closeDescriptor(pending.descriptor)
                }
            }
        }
        bind()
    }

    private fun bind() {
        if (closed.get() || bound) return
        try {
            bound = context.bindService(Intent(context, BoxUiService::class.java), connection, Context.BIND_AUTO_CREATE)
            check(bound) { "Unable to bind native UI backend" }
        } catch (error: Exception) {
            terminate(error)
        }
    }

    /** Native views use the same backend handlers and method codec as Dart. */
    suspend fun call(method: String, args: Any? = null): Any? = callChannel(BOX_CHANNEL, method, args)

    suspend fun callControl(method: String, args: Any? = null): Any? {
        withContext(Dispatchers.Main.immediate) { rememberControl(method, args) }
        return callChannel(HOST_CHANNEL, method, args)
    }

    private suspend fun callChannel(channel: String, method: String, args: Any?): Any? {
        val completion = CompletableDeferred<Any?>()
        val id = withContext(Dispatchers.Main.immediate) {
            check(!closed.get()) { "UI bridge is closed" }
            submit(channel, IpcPayload.writtenBytes(StandardMethodCodec.INSTANCE.encodeMethodCall(MethodCall(method, args)))) { bytes ->
                runCatching {
                    StandardMethodCodec.INSTANCE.decodeEnvelope(
                        IpcPayload.readableBuffer(bytes) ?: error("Native operation was not implemented: $method"),
                    )
                }.fold(completion::complete, completion::completeExceptionally)
            }
        }
        return try {
            completion.await()
        } finally {
            replies.remove(id)
        }
    }

    fun updateForeground(value: Boolean) {
        desiredForeground = value
        sendControl("ui.foreground", value)
    }

    fun sendIntent(intent: Intent) = sendControl("ui.intent", IpcPayload.marshalIntent(intent))

    fun sendControl(method: String, args: Any? = null) {
        scope.launch {
            rememberControl(method, args)
            if (!closed.get()) submit(HOST_CHANNEL,
                IpcPayload.writtenBytes(StandardMethodCodec.INSTANCE.encodeMethodCall(MethodCall(method, args))), null)
        }
    }

    private fun rememberControl(method: String, args: Any?) {
        if (method == "ui.foreground" && args is Boolean) desiredForeground = args
        if (method in setOf("ui.attach", "ui.configuration", "ui.installerPermission")) rememberedControls[method] = args
    }

    private fun rememberEventSubscription(bytes: ByteArray) {
        val method = runCatching { StandardMethodCodec.INSTANCE.decodeMethodCall(IpcPayload.readableBuffer(bytes)!!).method }
            .getOrNull()
        when (method) {
            "listen" -> eventListen = bytes
            "cancel" -> eventListen = null
        }
    }

    private fun restoreSession(reconnected: Boolean) {
        if (rememberedControls.containsKey("ui.attach")) {
            sendControl("ui.attach", rememberedControls["ui.attach"])
        }
        sendControl("ui.foreground", desiredForeground)
        rememberedControls.toMap().filterKeys { it != "ui.attach" }.forEach { (method, args) -> sendControl(method, args) }
        if (!reconnected) return
        val listen = eventListen ?: return
        // Dart's broadcast listener remains registered when the native process dies.
        submit(EVENTS_CHANNEL, listen, null)
        val envelope = StandardMethodCodec.INSTANCE.encodeSuccessEnvelope(mapOf("type" to "backendReconnected", "value" to null))
        engineMessenger.send(EVENTS_CHANNEL, envelope)
    }

    /** Keeps the binding alive until the host has finished cancellation, recovery and flushing. */
    suspend fun prepareForExit(backgroundOnly: Boolean = false) {
        callControl("ui.prepareExit", backgroundOnly)
    }

    suspend fun closeAndAwait() {
        prepareForExit()
        withContext(Dispatchers.Main.immediate) { close() }
    }

    private fun submit(channel: String, bytes: ByteArray?, reply: ((ByteArray?) -> Unit)?): Long {
        val id = if (reply == null) 0L else nextRequest.incrementAndGet()
        if (reply != null) replies[id] = reply
        // Capture this readiness generation so an old request cannot silently enter a new session.
        val ready = sessionReady
        scope.launch {
            try {
                val target = ready.await()
                if (closed.get()) error("UI bridge is closed")
                check(target === session) { "Native UI session changed" }
                sendToSession(target, bytes) { target.submit(channel, it, id) }
            } catch (error: Exception) {
                replies.remove(id)?.invoke(IpcPayload.errorBytes(error))
            }
        }
        return id
    }

    private fun sendToSession(target: IBoxUiSession, bytes: ByteArray?, send: (ParcelFileDescriptor?) -> Unit) {
        sender.send(bytes, onFailure = { error ->
            scope.launch { if (target === session) reconnect(error) }
        }) { if (target === session && !closed.get()) send(it) }
    }

    private fun receive(kind: Kind, channel: String?, descriptor: ParcelFileDescriptor?, id: Long, expectedGeneration: Long) {
        if (closed.get() || expectedGeneration != generation) {
            closeDescriptor(descriptor)
            return
        }
        val retained = try {
            IpcPayload.retain(descriptor)
        } catch (error: Exception) {
            scope.launch { if (expectedGeneration == generation) reconnect(error) }
            return
        }
        if (closed.get() || !incoming.trySend(Incoming(kind, channel, retained, id, expectedGeneration)).isSuccess) {
            closeDescriptor(retained)
        }
    }

    private fun dispatch(message: Incoming, bytes: ByteArray?) {
        if (message.kind != Kind.CLOSED) reconnectAttempts = 0
        when (message.kind) {
            Kind.MESSAGE -> {
                val target = session ?: return
                if (message.channel !in FLUTTER_CHANNELS) return
                engineMessenger.send(message.channel!!, IpcPayload.writtenBuffer(bytes)) { response ->
                    if (!closed.get() && message.id != 0L) {
                        sendToSession(target, IpcPayload.readableBytes(response)) { target.reply(message.id, it) }
                    }
                }
            }
            Kind.REPLY -> replies.remove(message.id)?.invoke(bytes)
            Kind.UI_REQUEST -> dispatchUiRequest(message.id, bytes)
            Kind.CLOSED -> {
                val error = IllegalStateException(message.reason ?: "Native UI session closed")
                if (message.recoverable) reconnect(error) else terminate(error)
            }
        }
    }

    private fun dispatchUiRequest(id: Long, bytes: ByteArray?) {
        val target = session ?: return
        val job = uiScope.launch {
            val response = try {
                val call = StandardMethodCodec.INSTANCE.decodeMethodCall(
                    IpcPayload.readableBuffer(bytes) ?: error("Missing UI operation"),
                )
                IpcPayload.writtenBytes(StandardMethodCodec.INSTANCE.encodeSuccessEnvelope(uiHandler(call.method, call.arguments)))
            } catch (error: Exception) {
                IpcPayload.errorBytes(error, "ui_operation")
            }
            if (!closed.get()) sendToSession(target, response) { target.replyUi(id, it) }
        }
        uiJobs.add(job)
        job.invokeOnCompletion { uiJobs.remove(job) }
    }

    private fun invalidate(error: Throwable, reconnecting: Boolean = false) {
        if (closed.get()) return
        reconnectJob?.cancel()
        reconnectJob = null
        generation++
        session = null
        _connected.value = false
        sessionReady.completeExceptionally(error)
        sessionReady = CompletableDeferred<IBoxUiSession>().apply {
            if (!reconnecting) completeExceptionally(error)
        }
        val bytes = IpcPayload.errorBytes(error)
        val pending = replies.values.toList()
        replies.clear()
        pending.forEach { reply -> runCatching { reply(bytes) } }
        uiJobs.toList().forEach { it.cancel() }
        Log.w("BoxUiIPC", "UI backend connection ended", error)
    }

    /** Fail old calls immediately; only future calls may enter the replacement session. */
    private fun reconnect(error: Throwable, waitForSystem: Boolean = false) {
        if (closed.get() || session == null && reconnectJob?.isActive == true) return
        val previous = session
        invalidate(error, reconnecting = true)
        runCatching { previous?.close() }
        reconnectAttempts++
        if (reconnectAttempts > 3) {
            sessionReady.completeExceptionally(error)
            releaseBinding()
            return
        }
        val expectedGeneration = generation
        val delayMs = if (waitForSystem) 1_000L else 250L * (1L shl (reconnectAttempts - 1))
        reconnectJob = scope.launch {
            delay(delayMs)
            if (closed.get() || expectedGeneration != generation || _connected.value) return@launch
            releaseBinding()
            bind()
        }
    }

    private fun terminate(error: Throwable) {
        val previous = session
        invalidate(error)
        runCatching { previous?.close() }
        releaseBinding()
    }

    private fun releaseBinding() {
        if (!bound) return
        bound = false
        runCatching { context.unbindService(connection) }
    }

    override fun close() {
        if (!closed.compareAndSet(false, true)) return
        FLUTTER_CHANNELS.forEach { engineMessenger.setMessageHandler(it, null) }
        val error = IllegalStateException("UI bridge is closed")
        val bytes = IpcPayload.errorBytes(error)
        val pending = replies.values.toList()
        replies.clear()
        pending.forEach { reply -> runCatching { reply(bytes) } }
        sessionReady.completeExceptionally(error)
        uiJobs.toList().forEach { it.cancel() }
        sender.close()
        incoming.close()
        val target = session
        session = null
        _connected.value = false
        runCatching { target?.close() }
        releaseBinding()
        scope.cancel()
    }
}
