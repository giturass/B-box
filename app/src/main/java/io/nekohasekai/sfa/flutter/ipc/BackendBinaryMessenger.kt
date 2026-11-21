package io.nekohasekai.sfa.flutter.ipc

import android.util.Log
import io.flutter.plugin.common.BinaryMessenger
import java.io.Closeable
import java.nio.ByteBuffer
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/** Runs the existing MethodChannel/EventChannel handlers in the native backend process. */
internal class BackendBinaryMessenger(
    private val sendToUi: (String, ByteArray?, Long) -> Unit,
) : BinaryMessenger, Closeable {
    private val handlers = ConcurrentHashMap<String, BinaryMessenger.BinaryMessageHandler>()
    private val replies = ConcurrentHashMap<Long, BinaryMessenger.BinaryReply>()
    private val nextRequest = AtomicLong()
    private val closed = AtomicBoolean()

    override fun send(channel: String, message: ByteBuffer?) = send(channel, message, null)

    override fun send(channel: String, message: ByteBuffer?, callback: BinaryMessenger.BinaryReply?) {
        if (closed.get()) {
            callback?.reply(null)
            return
        }
        require(channel in FLUTTER_CHANNELS) { "Unsupported backend Flutter channel: $channel" }
        val id = if (callback == null) 0L else nextRequest.incrementAndGet()
        if (callback != null) replies[id] = callback
        try {
            sendToUi(channel, IpcPayload.writtenBytes(message), id)
        } catch (error: Exception) {
            replies.remove(id)?.reply(null)
            Log.e("BoxUiIPC", "Unable to send backend channel message", error)
        }
    }

    override fun setMessageHandler(channel: String, handler: BinaryMessenger.BinaryMessageHandler?) {
        require(channel in FLUTTER_CHANNELS) { "Unsupported backend Flutter channel: $channel" }
        if (handler == null) handlers.remove(channel) else handlers[channel] = handler
    }

    fun dispatch(channel: String, bytes: ByteArray?, requestId: Long, replyToUi: (Long, ByteArray?) -> Unit) {
        val answered = AtomicBoolean()
        val reply = BinaryMessenger.BinaryReply { response ->
            if (answered.compareAndSet(false, true) && requestId != 0L) {
                replyToUi(requestId, IpcPayload.writtenBytes(response))
            }
        }
        if (closed.get()) {
            reply.reply(null)
            return
        }
        try {
            val handler = handlers[channel]
            if (handler == null) reply.reply(null)
            else handler.onMessage(IpcPayload.readableBuffer(bytes), reply)
        } catch (error: Exception) {
            Log.e("BoxUiIPC", "Unhandled backend channel message", error)
            reply.reply(null)
        }
    }

    fun receiveReply(requestId: Long, bytes: ByteArray?) {
        try {
            replies.remove(requestId)?.reply(IpcPayload.readableBuffer(bytes))
        } catch (error: Exception) {
            Log.e("BoxUiIPC", "Unhandled backend channel reply", error)
        }
    }

    override fun close() {
        if (!closed.compareAndSet(false, true)) return
        handlers.clear()
        val pending = replies.values.toList()
        replies.clear()
        pending.forEach { reply -> runCatching { reply.reply(null) } }
    }
}
