package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.Closeable
import java.io.File
import java.util.UUID
import java.util.concurrent.atomic.AtomicBoolean

internal interface CaptureBodyNative : Closeable {
    fun read(id: String, response: Boolean, offset: Long, raw: Boolean): String
    fun prepareView(path: String, id: String, response: Boolean, format: String): String
    fun readView(offset: Long): String
    fun cancel()
}

/** A visible body owns its decoder, file and cancellation, independently of recording mutations. */
internal class CaptureBodyReaders(
    private val directory: File,
    private val localSession: () -> Long = CommandTarget::localSession,
    private val checkSession: (Long) -> Unit = CommandTarget::requireLocalSession,
    private val createTask: () -> CaptureBodyNative = {
        val task = Libbox.newUICaptureBodyTask()
        object : CaptureBodyNative {
            override fun read(id: String, response: Boolean, offset: Long, raw: Boolean) = task.read(id, response, offset, raw)
            override fun prepareView(path: String, id: String, response: Boolean, format: String) = task.prepareView(path, id, response, format)
            override fun readView(offset: Long) = task.readView(offset)
            override fun cancel() = task.cancel()
            override fun close() = task.close()
        }
    },
) : Closeable {
    private class Reader(val session: Long, val id: String, val response: Boolean, val task: CaptureBodyNative, val path: File) {
        val mutex = Mutex()
        val jobs = mutableSetOf<Job>()
        var closed = false
    }

    private val access = Any()
    private val readers = mutableMapOf<String, Reader>()
    private var closed = false

    private companion object {
        val directoryAccess = Any()
        val preparedDirectories = mutableSetOf<String>()
        val viewFileName = Regex("[0-9a-fA-F-]{36}\\.view")
    }

    private fun prepareDirectory() = synchronized(directoryAccess) {
        check(directory.isDirectory || directory.mkdirs()) { "Unable to create body view directory" }
        if (preparedDirectories.add(directory.absolutePath)) {
            directory.listFiles()?.filter { it.isFile && (viewFileName.matches(it.name) ||
                it.name.startsWith("body-view-prepare-") && it.name.endsWith(".tmp")) }?.forEach { it.delete() }
        }
    }

    suspend fun read(readerId: String, id: String, response: Boolean, offset: Long, raw: Boolean): String =
        use(readerId, id, response) { it.task.read(id, response, offset, raw) }

    suspend fun prepareView(readerId: String, id: String, response: Boolean, format: String): String =
        use(readerId, id, response) {
            prepareDirectory()
            it.task.prepareView(it.path.absolutePath, id, response, format)
        }

    suspend fun readView(readerId: String, offset: Long): String {
        val target = synchronized(access) { readers[readerId]?.let { it.id to it.response } }
            ?: error("Body view is no longer open")
        return use(readerId, target.first, target.second) { it.task.readView(offset) }
    }

    private suspend fun use(readerId: String, id: String, response: Boolean, read: (Reader) -> String): String = coroutineScope {
        require(readerId.isNotBlank()) { "Missing body reader ID" }
        val owner = coroutineContext
        val session = localSession()
        val reader = synchronized(access) {
            check(!closed) { "Body readers are closed" }
            val current = readers[readerId]
            require(current == null || current.session == session && current.id == id && current.response == response) {
                "Body reader target changed"
            }
            (current ?: Reader(session, id, response, createTask(), File(directory, "${UUID.randomUUID()}.view"))
                .also { readers[readerId] = it }).also { it.jobs.add(owner.job) }
        }
        try {
            withCaptureBodyCancellation({ closeReader(readerId, reader) }) {
                reader.mutex.withLock {
                    withContext(Dispatchers.IO) {
                        owner.ensureActive()
                        checkSession(session)
                        val value = try { read(reader) } catch (error: Exception) {
                            owner.ensureActive()
                            throw error
                        }
                        owner.ensureActive()
                        checkSession(session)
                        value
                    }
                }
            }
        } catch (error: Exception) {
            if (runCatching { checkSession(session) }.isFailure) closeReader(readerId, reader)
            throw error
        } finally {
            val cleanup = synchronized(access) {
                reader.jobs.remove(owner.job)
                reader.closed && reader.jobs.isEmpty()
            }
            if (cleanup) withContext(NonCancellable + Dispatchers.IO) { reader.task.close() }
        }
    }

    fun closeReader(readerId: String): Boolean = closeReader(readerId, null)

    private fun closeReader(readerId: String, expected: Reader?): Boolean {
        val work = synchronized(access) {
            val reader = readers[readerId] ?: return false
            if (expected != null && reader !== expected) return false
            readers.remove(readerId)
            reader.closed = true
            reader to reader.jobs.toList()
        }
        // Cancel is a nonblocking native signal; Close never waits on active JNI.
        work.second.forEach { it.cancel() }
        work.first.task.cancel()
        if (work.second.isEmpty()) work.first.task.close()
        return true
    }

    fun cancelAll() {
        val ids = synchronized(access) { readers.keys.toList() }
        ids.forEach(::closeReader)
    }

    override fun close() {
        synchronized(access) { closed = true }
        cancelAll()
    }
}

/** Parent cancellation reaches a blocked native read before Dispatchers.IO returns. */
internal suspend fun <T> withCaptureBodyCancellation(cancel: () -> Unit, read: suspend () -> T): T = coroutineScope {
    val completed = AtomicBoolean(false)
    val cancellation = launch(Dispatchers.Unconfined, start = CoroutineStart.UNDISPATCHED) {
        try { awaitCancellation() } finally { if (!completed.get()) cancel() }
    }
    try {
        read()
    } finally {
        completed.set(true)
        cancellation.cancel()
    }
}
