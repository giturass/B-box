package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.util.UUID

/** A process-owned download. Flutter hosts observe it without owning its network job or APK path. */
internal class UpdateDownloadSession(
    private val scope: CoroutineScope,
    private val directory: File,
    private val download: suspend (Artifact, File, (Long, Long) -> Unit) -> Unit,
    private val verify: suspend (Artifact, File) -> Unit,
    private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {
    data class Artifact(
        val version: String,
        val versionCode: Long,
        val name: String,
        val url: String,
        val size: Long,
        val sha256: String,
        val releaseUrl: String,
        val notes: String,
    )

    data class State(
        val artifact: Artifact? = null,
        val status: String = "idle",
        val downloaded: Long = 0L,
        val total: Long = artifact?.size ?: 0L,
        val error: String? = null,
        val canInstall: Boolean = false,
    ) {
        fun toWire(): Map<String, Any?> = mapOf(
            "status" to status, "downloaded" to downloaded, "total" to total,
            "version" to artifact?.version, "versionCode" to artifact?.versionCode,
            "error" to error, "canInstall" to canInstall,
        )
    }

    private val lock = Any()
    private val mutableState = MutableStateFlow(State())
    val state: StateFlow<State> = mutableState.asStateFlow()
    private var job: Job? = null
    private var prepared: File? = null
    private var installing = false
    private var cancelling = false
    private var generation = 0L

    fun start(artifact: Artifact): State {
        scope.coroutineContext.ensureActive()
        require(artifact.versionCode > 0L && artifact.size in 1L until Long.MAX_VALUE && artifact.version.isNotBlank() &&
            artifact.sha256.matches(Regex("[0-9a-fA-F]{64}"))) { "Invalid update artifact" }
        val task: Job
        val snapshot: State
        synchronized(lock) {
            require(job == null && !installing && !cancelling && mutableState.value.status != "installing") { "An update operation is already in progress" }
            prepared?.delete()
            prepared = null
            val token = ++generation
            snapshot = State(artifact = artifact, status = "downloading")
            mutableState.value = snapshot
            task = scope.launch(start = CoroutineStart.LAZY) { runDownload(token, artifact) }
            job = task
        }
        task.start()
        return snapshot
    }

    private suspend fun runDownload(token: Long, artifact: Artifact) {
        var partial: File? = null
        var completed: File? = null
        var retained = false
        try {
            withContext(ioDispatcher) {
                require(directory.isDirectory || directory.mkdirs()) { "Unable to prepare update cache" }
                // These names are private and generated locally; release metadata never selects a path.
                directory.listFiles()?.filter { it.name.startsWith("bbox-update-") && (it.name.endsWith(".part") || it.name.endsWith(".apk")) }
                    ?.forEach { it.delete() }
                val id = UUID.randomUUID().toString()
                partial = File(directory, "bbox-update-$id.part")
                completed = File(directory, "bbox-update-$id.apk")
                var lastProgressMillis = 0L
                download(artifact, partial!!) { bytes, _ ->
                    val now = System.nanoTime() / 1_000_000
                    synchronized(lock) {
                        if (generation == token && mutableState.value.status == "downloading" &&
                            (lastProgressMillis == 0L || now - lastProgressMillis >= 100 || bytes >= artifact.size)) {
                            lastProgressMillis = now
                            mutableState.value = mutableState.value.copy(downloaded = bytes.coerceIn(0L, artifact.size))
                        }
                    }
                }
                currentCoroutineContext().ensureActive()
                synchronized(lock) {
                    if (generation == token) mutableState.value = mutableState.value.copy(status = "verifying", downloaded = artifact.size)
                }
                verify(artifact, partial!!)
                currentCoroutineContext().ensureActive()
                require(!completed!!.exists() && partial!!.renameTo(completed!!)) { "Unable to prepare verified APK" }
                val ownerContext = currentCoroutineContext()
                synchronized(lock) {
                    ownerContext.ensureActive()
                    if (generation == token) {
                        prepared = completed
                        retained = true
                    }
                }
            }
        } catch (error: CancellationException) {
            synchronized(lock) {
                if (generation == token) {
                    prepared = null
                    retained = false
                    mutableState.value = mutableState.value.copy(status = "cancelled", error = null, canInstall = false)
                }
            }
            throw error
        } catch (error: Exception) {
            synchronized(lock) {
                if (generation == token) mutableState.value = mutableState.value.copy(status = "error", error = error.message ?: "Update download failed", canInstall = false)
            }
        } finally {
            withContext(NonCancellable + ioDispatcher) {
                partial?.delete()
                if (!retained) completed?.delete()
            }
            synchronized(lock) {
                if (generation == token) {
                    job = null
                    if (retained) mutableState.value = mutableState.value.copy(status = "ready", canInstall = true)
                }
            }
        }
    }

    suspend fun cancel(): State {
        val task: Job?
        synchronized(lock) {
            require(!installing && !cancelling && mutableState.value.status != "installing") { "The system installer is in progress" }
            cancelling = true
            task = job
        }
        try {
            withContext(NonCancellable) {
                task?.cancelAndJoin()
                val file = synchronized(lock) {
                    // A job cancelled before its coroutine body starts never enters runDownload's finally block.
                    if (job === task) job = null
                    prepared.also { prepared = null }
                }
                withContext(ioDispatcher) { file?.delete() }
                synchronized(lock) {
                    mutableState.value = mutableState.value.copy(status = "cancelled", error = null, canInstall = false)
                }
            }
        } finally { synchronized(lock) { cancelling = false } }
        return state.value
    }

    /** The host may open a permission screen or system installer; an intent launch is never installation success. */
    suspend fun install(versionCode: Long, handoff: suspend (File) -> String): State {
        val artifact: Artifact
        val file: File
        synchronized(lock) {
            require(job == null && !installing && !cancelling && mutableState.value.status != "installing") { "An update operation is already in progress" }
            artifact = mutableState.value.artifact ?: error("No downloaded update")
            require(artifact.versionCode == versionCode) { "The prepared update changed; check again" }
            file = prepared ?: error("Download and verify the update first")
            installing = true
            mutableState.value = mutableState.value.copy(status = "verifying", error = null, canInstall = false)
        }
        var validated = false
        try {
            withContext(ioDispatcher) { verify(artifact, file) }
            currentCoroutineContext().ensureActive()
            validated = true
            synchronized(lock) { mutableState.value = mutableState.value.copy(status = "installing", canInstall = true) }
            val status = handoff(file)
            require(status in setOf("ready", "permissionRequired", "installing")) { "Invalid system installer result" }
            synchronized(lock) { mutableState.value = mutableState.value.copy(status = status, error = null, canInstall = true) }
        } catch (error: CancellationException) {
            // Host recreation must not lose a previously verified APK or resume installation by itself.
            synchronized(lock) { mutableState.value = mutableState.value.copy(status = "ready", error = null, canInstall = true) }
            throw error
        } catch (error: Exception) {
            if (!validated) {
                synchronized(lock) { prepared = null }
                withContext(NonCancellable + ioDispatcher) { file.delete() }
            }
            synchronized(lock) {
                mutableState.value = mutableState.value.copy(status = "error", error = error.message ?: "Unable to open the system installer", canInstall = validated)
            }
        } finally { synchronized(lock) { installing = false } }
        return state.value
    }

    fun permissionGranted() {
        synchronized(lock) {
            if (prepared != null && mutableState.value.status == "permissionRequired") {
                mutableState.value = mutableState.value.copy(status = "ready", error = null, canInstall = true)
            }
        }
    }
}
