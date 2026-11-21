package io.nekohasekai.sfa.flutter.ipc

import android.content.Context
import android.os.Binder
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.util.Log
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.StandardMethodCodec
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.Closeable
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/** One native host per Activity. Its work scope survives transport detachment for recovery. */
internal class BackendSession(
    private val context: Context,
    private val callback: IBoxUiCallback,
    private val onDisconnected: (BackendSession) -> Unit,
) : Closeable, IBinder.DeathRecipient {
    val supervisorJob = SupervisorJob()
    val scope = CoroutineScope(supervisorJob + Dispatchers.Main.immediate)
    private val main = Handler(Looper.getMainLooper())
    private val detached = AtomicBoolean()
    private val backendReady = CompletableDeferred<Unit>()
    private var backend: Closeable? = null
    private val _foreground = MutableStateFlow(false)
    val foreground = _foreground.asStateFlow()
    val isConnected: Boolean get() = !detached.get()

    var controlHandler: suspend (String, Any?) -> Any? = { method, _ ->
        error("Unknown UI host operation: $method")
    }

    /** The host cancels and joins bridge operations, including NonCancellable recovery, then flushes. */
    var prepareExitHandler: suspend () -> Unit = {}

    private val uiRequests = ConcurrentHashMap<Long, CompletableDeferred<Any?>>()
    private val nextUiRequest = AtomicLong()
    private val sender = IpcSender(context, scope) { disconnect("UI transport failed") }
    val messenger = BackendBinaryMessenger { channel, bytes, id ->
        if (isConnected) sender.send(bytes) { callback.onMessage(channel, it, id) }
    }

    private enum class Kind { SUBMIT, REPLY, UI_REPLY }
    private data class Incoming(val kind: Kind, val channel: String?, val descriptor: ParcelFileDescriptor?, val id: Long)
    private val incoming = Channel<Incoming>(Channel.UNLIMITED)

    val binder: IBoxUiSession = object : IBoxUiSession.Stub() {
        override fun submit(channel: String, message: ParcelFileDescriptor?, requestId: Long) {
            requireCaller()
            receive(Kind.SUBMIT, channel, message, requestId)
        }

        override fun reply(requestId: Long, message: ParcelFileDescriptor?) {
            requireCaller()
            receive(Kind.REPLY, null, message, requestId)
        }

        override fun replyUi(requestId: Long, message: ParcelFileDescriptor?) {
            requireCaller()
            receive(Kind.UI_REPLY, null, message, requestId)
        }

        override fun close() {
            requireCaller()
            disconnect("UI session closed", recoverable = false)
        }
    }

    init {
        scope.launch(Dispatchers.IO) {
            try {
                for (message in incoming) {
                    if (!isConnected) {
                        closeDescriptor(message.descriptor)
                        continue
                    }
                    try {
                        val bytes = IpcPayload.read(message.descriptor)
                        if (message.kind == Kind.SUBMIT) backendReady.await()
                        withContext(Dispatchers.Main.immediate) {
                            if (isConnected) dispatch(message, bytes)
                        }
                    } catch (error: Exception) {
                        closeDescriptor(message.descriptor)
                        if (isConnected) disconnect("Unable to read UI message")
                    }
                }
            } finally {
                while (true) {
                    val pending = incoming.tryReceive().getOrNull() ?: break
                    closeDescriptor(pending.descriptor)
                }
            }
        }
    }

    fun watchUiProcess() {
        callback.asBinder().linkToDeath(this, 0)
    }

    fun attachBackend(value: Closeable) {
        if (!isConnected) {
            value.close()
            return
        }
        backend = value
        backendReady.complete(Unit)
    }

    suspend fun requestUi(method: String, args: Any? = null): Any? {
        check(isConnected) { "UI process is no longer connected" }
        val id = nextUiRequest.incrementAndGet()
        val result = CompletableDeferred<Any?>()
        uiRequests[id] = result
        try {
            val bytes = IpcPayload.writtenBytes(StandardMethodCodec.INSTANCE.encodeMethodCall(MethodCall(method, args)))
            if (!isConnected) error("UI process is no longer connected")
            sender.send(bytes) { callback.onUiRequest(id, it) }
            return result.await()
        } finally {
            uiRequests.remove(id)
        }
    }

    private fun requireCaller() {
        check(Binder.getCallingUid() == context.applicationInfo.uid) { "UI IPC is private to this application" }
    }

    private fun receive(kind: Kind, channel: String?, descriptor: ParcelFileDescriptor?, id: Long) {
        val retained = try {
            IpcPayload.retain(descriptor)
        } catch (error: Exception) {
            disconnect("Unable to retain UI message")
            return
        }
        if (!isConnected || !incoming.trySend(Incoming(kind, channel, retained, id)).isSuccess) {
            closeDescriptor(retained)
        }
    }

    private fun dispatch(message: Incoming, bytes: ByteArray?) {
        when (message.kind) {
            Kind.SUBMIT -> {
                if (message.channel == HOST_CHANNEL) dispatchControl(bytes, message.id)
                else messenger.dispatch(message.channel.orEmpty(), bytes, message.id, ::replyToUi)
            }
            Kind.REPLY -> messenger.receiveReply(message.id, bytes)
            Kind.UI_REPLY -> {
                val result = uiRequests.remove(message.id) ?: return
                runCatching {
                    StandardMethodCodec.INSTANCE.decodeEnvelope(
                        IpcPayload.readableBuffer(bytes) ?: error("UI operation was not implemented"),
                    )
                }.fold(result::complete, result::completeExceptionally)
            }
        }
    }

    private fun dispatchControl(bytes: ByteArray?, id: Long) {
        scope.launch {
            val response = try {
                val call = StandardMethodCodec.INSTANCE.decodeMethodCall(
                    IpcPayload.readableBuffer(bytes) ?: error("Missing UI host operation"),
                )
                val value = when (call.method) {
                    "ui.prepareExit" -> {
                        // A cancelled background timer can leave its Binder
                        // request queued behind the return-to-foreground event.
                        if (call.arguments != true || !_foreground.value) prepareExitHandler()
                        null
                    }
                    "ui.foreground" -> {
                        _foreground.value = call.arguments as? Boolean ?: error("Missing foreground state")
                        controlHandler(call.method, call.arguments)
                    }
                    else -> controlHandler(call.method, call.arguments)
                }
                IpcPayload.writtenBytes(StandardMethodCodec.INSTANCE.encodeSuccessEnvelope(value))
            } catch (error: Exception) {
                IpcPayload.errorBytes(error)
            }
            if (id != 0L) replyToUi(id, response)
        }
    }

    private fun replyToUi(id: Long, bytes: ByteArray?) {
        if (isConnected) sender.send(bytes) { callback.onReply(id, it) }
    }

    override fun binderDied() = disconnect("UI process exited", recoverable = false)

    fun disconnect(reason: String, recoverable: Boolean = true) {
        if (!detached.compareAndSet(false, true)) return
        // Publish transport death first so blocked UI requests fail and profile recovery can run.
        val failure = IllegalStateException(reason)
        uiRequests.values.forEach { it.completeExceptionally(failure) }
        uiRequests.clear()
        backendReady.completeExceptionally(failure)
        sender.close()
        incoming.close()
        runCatching { callback.asBinder().unlinkToDeath(this, 0) }
        main.post {
            _foreground.value = false
            messenger.close()
            onDisconnected(this)
            runCatching { callback.onClosed(reason, recoverable) }
            val owner = backend
            backend = null
            if (owner == null) completeClose()
            else runCatching { owner.close() }.onFailure {
                Log.e("BoxUiIPC", "Unable to detach native UI host", it)
            }
        }
    }

    /** Called by the host after cancelling/joining operations and closing lifecycle resources. */
    fun completeClose() {
        if (isConnected) disconnect("Native UI host closed", recoverable = false)
        scope.cancel()
    }

    override fun close() = disconnect("Native UI session closed", recoverable = false)
}
