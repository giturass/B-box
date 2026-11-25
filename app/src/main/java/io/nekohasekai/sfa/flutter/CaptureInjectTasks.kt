package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.job
import kotlinx.coroutines.withContext
import java.io.Closeable

/** One blocking native call per request ID; cancellation reaches JNI without holding recording locks. */
internal open class CaptureCallTasks<N : Closeable>(
    private val name: String,
    private val cancelled: String,
    private val localSession: () -> Long,
    private val checkSession: (Long) -> Unit,
    private val createTask: () -> N,
) : Closeable {
    private class Call<N : Closeable>(val job: Job) {
        private val access = Any()
        private var native: N? = null
        private var closed = false

        fun attach(task: N) {
            val cancel = synchronized(access) {
                check(native == null) { "A request already owns a task" }
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
    private val pending = mutableMapOf<String, Call<N>>()
    private var closed = false

    protected suspend fun <T> perform(requestId: String, work: (N) -> T): T = coroutineScope {
        require(requestId.isNotBlank() && requestId.length <= 128 && requestId.none { it.isISOControl() }) { "Invalid $name request ID" }
        val owner = coroutineContext
        val session = localSession()
        val call = synchronized(access) {
            check(!closed) { "Capture $name is closed" }
            require(requestId !in pending) { "This $name request is already running" }
            Call<N>(owner.job).also { pending[requestId] = it }
        }
        try {
            withCaptureBodyCancellation(call::close) {
                withContext(Dispatchers.IO) {
                    owner.ensureActive()
                    checkSession(session)
                    val native = createTask()
                    call.attach(native)
                    owner.ensureActive()
                    checkSession(session)
                    val result = try { work(native) }
                        catch (failure: Exception) { owner.ensureActive(); throw failure }
                    owner.ensureActive()
                    checkSession(session)
                    result
                }
            }
        } finally {
            synchronized(access) { if (pending[requestId] === call) pending.remove(requestId) }
            call.close()
        }
    }

    fun cancel(requestId: String): Boolean {
        val call = synchronized(access) { pending[requestId] } ?: return false
        cancel(call)
        return true
    }

    private fun cancel(call: Call<N>) {
        call.job.cancel(CancellationException(cancelled))
        call.close()
    }

    fun cancelAll() {
        val calls = synchronized(access) { pending.values.toList() }
        calls.forEach(::cancel)
    }

    override fun close() {
        synchronized(access) { closed = true }
        cancelAll()
    }
}

internal interface CaptureInjectNative : Closeable {
    fun inject(streamId: String, direction: String, opcode: Int, dataBase64: String)
}

/** A stream write can block until its flow accepts the bytes. */
internal class CaptureInjectTasks(
    localSession: () -> Long = CommandTarget::localSession,
    checkSession: (Long) -> Unit = CommandTarget::requireLocalSession,
    createTask: () -> CaptureInjectNative = {
        val task = Libbox.newUICaptureInjectTask()
        object : CaptureInjectNative {
            override fun inject(streamId: String, direction: String, opcode: Int, dataBase64: String) {
                task.inject(streamId, direction, opcode, dataBase64)
            }
            override fun close() = task.close()
        }
    },
) : CaptureCallTasks<CaptureInjectNative>("injection", "Stream injection cancelled", localSession, checkSession, createTask) {
    suspend fun inject(requestId: String, streamId: String, direction: String, opcode: Int, dataBase64: String): Boolean {
        require(streamId.isNotBlank()) { "Missing stream ID" }
        require(direction == "client" || direction == "server") { "Invalid stream direction" }
        require(opcode in 0..2) { "Invalid stream opcode" }
        return perform(requestId) { it.inject(streamId, direction, opcode, dataBase64); true }
    }
}

internal interface CaptureReplayNative : Closeable {
    fun replay(id: String, optionsJSON: String): String
}

/** A replay lasts until its response ends; it is bound to the service instance that created it. */
internal class CaptureReplayTasks(
    localSession: () -> Long = CommandTarget::localSession,
    checkSession: (Long) -> Unit = CommandTarget::requireLocalSession,
    createTask: () -> CaptureReplayNative = {
        val server = BoxService.dashboardServer ?: error("Start the local service before replaying a request")
        val task = server.newUICaptureReplayTask()
        object : CaptureReplayNative {
            override fun replay(id: String, optionsJSON: String): String = task.replay(id, optionsJSON)
            override fun close() = task.close()
        }
    },
) : CaptureCallTasks<CaptureReplayNative>("replay", "Request replay cancelled", localSession, checkSession, createTask) {
    suspend fun replay(requestId: String, id: String, optionsJSON: String): String {
        require(id.isNotBlank()) { "Missing request ID" }
        return perform(requestId) { it.replay(id, optionsJSON) }
    }
}

/** History reads depend on saved records; active stream writes and replays have their own lifetime. */
internal class CaptureTasks(
    private val bodies: CaptureBodyReaders,
    private val injections: CaptureInjectTasks,
    private val replays: CaptureReplayTasks,
) : Closeable {
    fun clearRecords(clear: () -> Unit) {
        bodies.cancelAll()
        clear()
    }

    fun cancelAll() {
        bodies.cancelAll()
        injections.cancelAll()
        replays.cancelAll()
    }

    override fun close() {
        bodies.close()
        injections.close()
        replays.close()
    }
}
