package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.UIScriptTask
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.Closeable
import java.io.File

internal data class ScriptRunRequest(
    val requestId: String,
    val profileId: Long,
    val revision: String,
    val type: String,
    val index: Int,
) {
    fun validate() {
        require(requestId.isNotBlank() && requestId.length <= 128 && requestId.none { it.isISOControl() }) { "Invalid script request" }
        require(profileId > 0 && revision.matches(Regex("[0-9a-f]{64}"))) { "Refresh the selected profile before running its script" }
        require(type in setOf("cron", "event", "generic") && index >= 0) { "Select a Cron, event or generic script" }
    }

    companion object {
        fun read(args: Map<String, Any?>): ScriptRunRequest {
            require(args.keys.all { it in setOf("requestId", "profileId", "revision", "type", "index") }) { "Invalid script argument" }
            fun string(key: String) = args[key] as? String ?: error("Missing script argument: $key")
            val index = args["index"] as? Number ?: error("Missing script argument: index")
            require(index.toLong() in 0..Int.MAX_VALUE.toLong() && index.toDouble() == index.toInt().toDouble()) { "Invalid script index" }
            val profileId = args["profileId"] as? Number ?: error("Missing script argument: profileId")
            require(profileId.toDouble() == profileId.toLong().toDouble()) { "Invalid script profile" }
            return ScriptRunRequest(string("requestId"), profileId.toLong(), string("revision"), string("type"), index.toInt())
        }
    }
}

internal interface ScriptDebugTask : Closeable {
    fun awaitResult(): Map<String, Any?>
    fun awaitClosed()
}

/** Each host owns its requests. Cancellation reaches JNI while IO is blocked. */
internal class BoxScripts<T>(
    private val prepare: suspend (ScriptRunRequest, (ScriptDebugTask) -> Unit) -> T,
    private val validate: suspend (T) -> Unit,
) : Closeable {
    private class Active(val id: String, val job: Job) {
        var task: ScriptDebugTask? = null
    }

    private val access = Any()
    private val active = mutableMapOf<String, Active>()
    private var closed = false

    private fun begin(id: String, job: Job): Active = synchronized(access) {
        check(!closed) { "Script debugging is no longer available" }
        require(id !in active) { "This script request is already running" }
        Active(id, job).also { active[id] = it }
    }

    private fun attach(operation: Active, task: ScriptDebugTask) {
        val cancelled = synchronized(access) {
            check(operation.task == null) { "A script request already owns a task" }
            operation.task = task
            closed || !operation.job.isActive
        }
        if (cancelled) cancel(operation)
    }

    suspend fun run(request: ScriptRunRequest): Map<String, Any?> = coroutineScope {
        // Register before waiting for any configuration/bridge lock, so an
        // editor's cancellation also reaches a request still being prepared.
        val owner = coroutineContext
        val operation = begin(request.requestId, owner.job)
        try {
            request.validate()
            val target = prepare(request) { task -> attach(operation, task) }
            owner.ensureActive()
            val task = synchronized(access) { checkNotNull(operation.task) { "The script task was not created" } }
            val result = withScriptDebugTask(task::close, task::awaitResult)
            owner.ensureActive()
            validate(target)
            result
        } finally {
            val task = synchronized(access) { operation.task }
            try {
                task?.close()
            } finally {
                try {
                    // The native worker can exist even if cancellation prevented
                    // prepare from returning or prevented awaitResult from starting.
                    if (task != null) withContext(NonCancellable + Dispatchers.IO) { task.awaitClosed() }
                } finally {
                    synchronized(access) {
                        if (active[operation.id] === operation) active.remove(operation.id)
                    }
                }
            }
        }
    }

    fun cancel(requestId: String): Boolean {
        val operation = synchronized(access) { active[requestId] } ?: return false
        cancel(operation)
        return true
    }

    private fun cancel(operation: Active) {
        val task = synchronized(access) { operation.task }
        // Native cancellation can immediately release awaitResult. Mark the
        // owner first so that result cannot pass the active-state checks.
        operation.job.cancel(CancellationException("Script debugging cancelled"))
        task?.close()
    }

    fun cancelAll() {
        val requests = synchronized(access) { active.values.toList() }
        requests.forEach(::cancel)
    }

    override fun close() {
        synchronized(access) { closed = true }
        cancelAll()
    }
}

internal suspend fun <T> withScriptDebugTask(close: () -> Unit, wait: () -> T): T = coroutineScope {
    val owner = coroutineContext
    val cancellation = launch(Dispatchers.Unconfined, start = CoroutineStart.UNDISPATCHED) {
        try { awaitCancellation() } finally { close() }
    }
    try {
        withContext(Dispatchers.IO) {
            owner.ensureActive()
            try { wait() } catch (error: Exception) {
                owner.ensureActive()
                throw error
            }
        }
    } finally {
        cancellation.cancel()
    }
}

internal data class ScriptDebugProfile(val request: ScriptRunRequest, val session: Long, val config: BoxService.DashboardConfig)

internal data class ScriptServiceState(
    val status: Status,
    val remoteId: Long?,
    val config: BoxService.DashboardConfig?,
    val applying: Boolean,
)

private class LibboxScriptDebugTask(private val task: UIScriptTask) : ScriptDebugTask {
    override fun close() = task.close()
    override fun awaitClosed() = task.join()
    override fun awaitResult(): Map<String, Any?> {
        val value = JSONObject(task.run())
        val logs = value.getJSONArray("logs")
        return mapOf("result" to value.getString("result"), "error" to value.getString("error"),
            "logs" to (0 until logs.length()).map(logs::getString))
    }
}

internal suspend fun prepareProfileScript(
    request: ScriptRunRequest,
    session: Long,
    created: (ScriptDebugTask) -> Unit,
): ScriptDebugProfile {
    currentCoroutineContext().ensureActive()
    CommandTarget.requireLocalSession(session)
    require(BoxService.currentStatus == Status.Started && !BoxService.isApplyingProfile) { "Wait for the local service to finish applying its profile" }
    return ProfileContent.mutex.withLock {
        ProfileContent.snapshotMutex.withLock {
            currentCoroutineContext().ensureActive()
            CommandTarget.requireLocalSession(session)
            require(BoxService.currentStatus == Status.Started && !BoxService.isApplyingProfile) { "Wait for the local service to finish applying its profile" }
            val config = BoxService.dashboardConfig.value ?: error("The local service is not available")
            require(request.profileId == Settings.selectedProfile && request.profileId == config.profileId) { "Run scripts from the current local profile" }
            require(request.revision == ProfileContent.revision(config.content)) { "The running profile changed; refresh it before debugging" }
            val profile = ProfileManager.get(request.profileId) ?: error("The selected profile no longer exists")
            require(request.revision == ProfileContent.revision(File(profile.typed.path).readText())) { "The saved profile changed; apply it before debugging" }
            require(BoxService.dashboardServer === config.server && BoxService.dashboardConfig.value === config && !BoxService.isApplyingProfile) {
                "The local service changed while preparing its script"
            }
            CommandTarget.requireLocalSession(session)
            currentCoroutineContext().ensureActive()
            val task = LibboxScriptDebugTask(config.server.newUIScriptTask(request.type, request.index))
            try {
                // Save the holder before withContext can discard a cancelled result.
                created(task)
                currentCoroutineContext().ensureActive()
                ScriptDebugProfile(request, session, config)
            } catch (error: Exception) {
                task.close()
                throw error
            }
        }
    }
}

internal suspend fun validateProfileScript(target: ScriptDebugProfile) = ProfileContent.mutex.withLock {
    ProfileContent.snapshotMutex.withLock {
        CommandTarget.requireLocalSession(target.session)
        require(BoxService.currentStatus == Status.Started && !BoxService.isApplyingProfile &&
            BoxService.dashboardConfig.value === target.config && BoxService.dashboardServer === target.config.server &&
            Settings.selectedProfile == target.request.profileId) { "The local profile changed during script debugging" }
        val profile = ProfileManager.get(target.request.profileId) ?: error("The selected profile no longer exists")
        require(ProfileContent.revision(File(profile.typed.path).readText()) == target.request.revision) { "The saved profile changed during script debugging" }
    }
}
