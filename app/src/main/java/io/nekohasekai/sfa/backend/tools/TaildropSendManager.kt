package io.nekohasekai.sfa.backend.tools

import android.content.ContentResolver
import android.content.Intent
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.OpenableColumns
import androidx.annotation.VisibleForTesting
import androidx.core.content.ContextCompat
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.TaildropSendHandler
import io.nekohasekai.libbox.TaildropSendOptions
import io.nekohasekai.libbox.TaildropSendSession
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.bg.TaildropSendService
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.ByteArrayInputStream
import java.io.File
import java.io.FileNotFoundException
import java.io.InputStream

class TaildropSendFile(
    val name: String,
    private val descriptor: ParcelFileDescriptor?,
    private val text: ByteArray? = null,
) {
    val size: Long = text?.size?.toLong() ?: descriptor!!.statSize

    fun open(): InputStream = if (text != null) {
        ByteArrayInputStream(text)
    } else {
        ParcelFileDescriptor.AutoCloseInputStream(descriptor!!)
    }

    fun close() {
        descriptor?.let { runCatching { it.close() } }
    }
}

data class TaildropSendFileState(
    val name: String,
    val size: Long,
    val sentBytes: Long = 0,
    val completed: Boolean = false,
)

data class TaildropSendState(
    val id: Long,
    val endpointTag: String,
    val peerName: String,
    val files: List<TaildropSendFileState>,
    val finished: Boolean = false,
    val errorMessage: String? = null,
)

object TaildropSendManager {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val _sessions = MutableStateFlow<List<TaildropSendState>>(emptyList())
    val sessions: StateFlow<List<TaildropSendState>> = _sessions.asStateFlow()

    private val access = Any()
    private var nextID = 0L
    private val jobs = mutableMapOf<Long, Job>()
    private val holders = mutableMapOf<Long, SessionHolder>()

    fun openURIs(uris: List<Uri>): List<TaildropSendFile> {
        val files = mutableListOf<TaildropSendFile>()
        try {
            for (uri in uris) {
                if (uri.scheme != ContentResolver.SCHEME_CONTENT) {
                    throw FileNotFoundException("unsupported share source: $uri")
                }
                val resolver = Application.application.contentResolver
                val descriptor = resolver.openFileDescriptor(uri, "r")
                    ?: throw FileNotFoundException(uri.toString())
                try {
                    files.add(TaildropSendFile(displayName(resolver, uri), descriptor))
                } catch (error: Exception) {
                    runCatching { descriptor.close() }
                    throw error
                }
            }
        } catch (e: Exception) {
            for (file in files) {
                file.close()
            }
            throw e
        }
        return files
    }

    fun createTextFile(name: String, text: String): TaildropSendFile = TaildropSendFile(name, null, text.encodeToByteArray())

    fun send(endpointTag: String, peerStableID: String, peerName: String, files: List<TaildropSendFile>): Long =
        send(endpointTag, peerStableID, peerName, files) {
            ContextCompat.startForegroundService(
                Application.application,
                Intent(Application.application, TaildropSendService::class.java),
            )
        }

    /** On success the manager owns the files; rejected startup releases all staged resources. */
    @VisibleForTesting
    internal fun send(
        endpointTag: String,
        peerStableID: String,
        peerName: String,
        files: List<TaildropSendFile>,
        startService: () -> Unit,
    ): Long {
        if (files.isEmpty()) return -1L
        val openedFiles = files.toList()
        val id: Long
        val holder = SessionHolder(openedFiles)
        val job: Job
        synchronized(access) {
            id = ++nextID
            holders[id] = holder
            job = scope.launch(start = CoroutineStart.LAZY) {
                runSession(id, holder, endpointTag, peerStableID, openedFiles)
            }
            jobs[id] = job
            _sessions.update { sessions ->
                sessions + TaildropSendState(
                    id = id,
                    endpointTag = endpointTag,
                    peerName = peerName,
                    files = openedFiles.map { TaildropSendFileState(name = it.name, size = it.size) },
                )
            }
            // A cancelled LAZY coroutine never enters runSession's finally block.
            job.invokeOnCompletion {
                synchronized(access) {
                    jobs.remove(id)
                    holders.remove(id)
                }
                holder.close()
            }
        }
        try {
            startService()
            if (!job.start()) throw CancellationException("Taildrop send was cancelled before starting")
            return id
        } catch (error: Exception) {
            synchronized(access) {
                jobs.remove(id)
                holders.remove(id)
                _sessions.update { sessions -> sessions.filterNot { it.id == id } }
            }
            job.cancel()
            holder.close()
            throw error
        }
    }

    fun cancel(id: Long) {
        val job: Job?
        val holder: SessionHolder?
        synchronized(access) {
            job = jobs.remove(id)
            holder = holders.remove(id)
        }
        dismiss(id)
        job?.cancel()
        scope.launch { holder?.close() }
    }

    fun cancelAll() {
        for (session in _sessions.value) {
            if (!session.finished) {
                cancel(session.id)
            }
        }
    }

    fun dismiss(id: Long) {
        _sessions.update { sessions -> sessions.filterNot { it.id == id } }
    }

    private class SessionHolder(private val files: List<TaildropSendFile>) {
        private var session: TaildropSendSession? = null
        private var closed = false

        @Synchronized
        fun set(newSession: TaildropSendSession) {
            if (closed) {
                runCatching { newSession.close() }
                return
            }
            session = newSession
        }

        @Synchronized
        fun close() {
            if (closed) return
            closed = true
            val current = session
            session = null
            runCatching { current?.close() }
            files.forEach(TaildropSendFile::close)
        }
    }

    private suspend fun runSession(
        id: Long,
        holder: SessionHolder,
        endpointTag: String,
        peerStableID: String,
        files: List<TaildropSendFile>,
    ) {
        try {
            val options = TaildropSendOptions()
            options.endpointTag = endpointTag
            options.peerStableID = peerStableID
            for (file in files) {
                options.addFile(file.name, file.size)
            }
            val finish = CompletableDeferred<String>()
            val session = CommandTarget.standaloneClient().sendTaildropFiles(
                options,
                object : TaildropSendHandler {
                    override fun onProgress(fileIndex: Int, sentBytes: Long) {
                        updateFile(id, fileIndex) { copy(sentBytes = sentBytes) }
                    }

                    override fun onFileCompleted(fileIndex: Int, sentBytes: Long) {
                        updateFile(id, fileIndex) { copy(size = sentBytes, sentBytes = sentBytes, completed = true) }
                    }

                    override fun onFinish(errorMessage: String) {
                        finish.complete(errorMessage)
                    }
                },
            )
            holder.set(session)

            for (file in files) {
                currentCoroutineContext().ensureActive()
                file.open().use { writeFile(session, it) }
            }

            val errorMessage = finish.await()
            updateSession(id) {
                copy(finished = true, errorMessage = errorMessage.ifEmpty { null })
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            updateSession(id) {
                copy(
                    finished = true,
                    errorMessage = e.message ?: e.toString(),
                )
            }
        } finally {
            holder.close()
        }
    }

    private suspend fun writeFile(session: TaildropSendSession, input: InputStream) {
        val chunkSize = Libbox.TaildropChunkSize.toInt()
        var reachedEnd = false
        while (!reachedEnd) {
            currentCoroutineContext().ensureActive()
            val chunk = ByteArray(chunkSize)
            var filled = 0
            while (filled < chunkSize) {
                currentCoroutineContext().ensureActive()
                val read = input.read(chunk, filled, chunkSize - filled)
                if (read < 0) {
                    reachedEnd = true
                    break
                }
                filled += read
            }
            if (filled > 0) {
                session.writeChunk(if (filled == chunkSize) chunk else chunk.copyOf(filled))
            }
        }
        currentCoroutineContext().ensureActive()
        session.finishFile()
    }

    private fun displayName(resolver: ContentResolver, uri: Uri): String {
        resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
            if (cursor.moveToFirst() && !cursor.isNull(0)) {
                val name = cursor.getString(0)
                if (!name.isNullOrEmpty()) return File(name).name
            }
        }
        return uri.lastPathSegment?.let { File(it).name }?.takeIf { it.isNotEmpty() } ?: "file"
    }

    private fun updateSession(id: Long, reducer: TaildropSendState.() -> TaildropSendState) {
        _sessions.update { sessions ->
            sessions.map { if (it.id == id) it.reducer() else it }
        }
    }

    private fun updateFile(
        id: Long,
        fileIndex: Int,
        reducer: TaildropSendFileState.() -> TaildropSendFileState,
    ) {
        updateSession(id) {
            if (fileIndex !in files.indices) {
                this
            } else {
                copy(
                    files = files.mapIndexed { index, file ->
                        if (index == fileIndex) file.reducer() else file
                    },
                )
            }
        }
    }
}
