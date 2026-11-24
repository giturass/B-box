package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.job
import kotlinx.coroutines.withContext
import java.io.Closeable

internal interface CaptureInjectNative : Closeable {
    fun inject(streamId: String, direction: String, opcode: Int, dataBase64: String)
}

/** A stream write can block; cancellation reaches JNI without holding recording locks. */
internal class CaptureInjectTasks(
    private val localSession: () -> Long = CommandTarget::localSession,
    private val checkSession: (Long) -> Unit = CommandTarget::requireLocalSession,
    private val createTask: () -> CaptureInjectNative = {
        val task = Libbox.newUICaptureInjectTask()
        object : CaptureInjectNative {
            override fun inject(streamId: String, direction: String, opcode: Int, dataBase64: String) {
                task.inject(streamId, direction, opcode, dataBase64)
            }
            override fun close() = task.close()
        }
    },
) : Closeable {
    private class Injection(val id: String, val job: Job) {
        private val access = Any()
        private var native: CaptureInjectNative? = null
        private var closed = false

        fun attach(task: CaptureInjectNative) {
            val cancel = synchronized(access) {
                check(native == null) { "An injection already owns a task" }
                native = task
                closed
            }
            if (cancel) task.close()
        }

        fun close() {
            val task = synchronized(access) {
                if (closed) return
                closed = true
                native
            }
            task?.close()
        }
    }

    private val access = Any()
    private val pending = mutableMapOf<String, Injection>()
    private var closed = false

    suspend fun inject(requestId: String, streamId: String, direction: String, opcode: Int, dataBase64: String): Boolean = coroutineScope {
        require(requestId.isNotBlank() && requestId.length <= 128 && requestId.none { it.isISOControl() }) { "Invalid injection request ID" }
        require(streamId.isNotBlank()) { "Missing stream ID" }
        require(direction == "client" || direction == "server") { "Invalid stream direction" }
        require(opcode in 0..2) { "Invalid stream opcode" }
        val owner = coroutineContext
        val session = localSession()
        val injection = synchronized(access) {
            check(!closed) { "Capture injection is closed" }
            require(requestId !in pending) { "This injection request is already running" }
            Injection(requestId, owner.job).also { pending[requestId] = it }
        }
        try {
            withCaptureBodyCancellation(injection::close) {
                withContext(Dispatchers.IO) {
                    owner.ensureActive()
                    checkSession(session)
                    val native = createTask()
                    injection.attach(native)
                    owner.ensureActive()
                    checkSession(session)
                    try { native.inject(streamId, direction, opcode, dataBase64) }
                    catch (failure: Exception) { owner.ensureActive(); throw failure }
                    owner.ensureActive()
                    checkSession(session)
                    true
                }
            }
        } finally {
            synchronized(access) { if (pending[requestId] === injection) pending.remove(requestId) }
            injection.close()
        }
    }

    fun cancel(requestId: String): Boolean {
        val injection = synchronized(access) { pending[requestId] } ?: return false
        cancel(injection)
        return true
    }

    private fun cancel(injection: Injection) {
        injection.job.cancel(CancellationException("Stream injection cancelled"))
        injection.close()
    }

    fun cancelAll() {
        val tasks = synchronized(access) { pending.values.toList() }
        tasks.forEach(::cancel)
    }

    override fun close() {
        synchronized(access) { closed = true }
        cancelAll()
    }
}

/** History reads depend on saved records; active stream writes have their own lifetime. */
internal class CaptureTasks(
    private val bodies: CaptureBodyReaders,
    private val injections: CaptureInjectTasks,
) : Closeable {
    fun clearRecords(clear: () -> Unit) {
        bodies.cancelAll()
        clear()
    }

    fun cancelAll() {
        bodies.cancelAll()
        injections.cancelAll()
    }

    override fun close() {
        bodies.close()
        injections.close()
    }
}
