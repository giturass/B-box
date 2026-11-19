package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.UIWARPRegistrationTask
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Profile
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.Closeable
import java.io.File
import java.util.UUID

internal class BoxWarpException(val code: String, message: String, val details: Map<String, Any?>? = null) : Exception(message)

internal data class WarpProfileSnapshot(val profileId: Long, val revision: String, val session: Long, val path: String)

/** Registration never holds a configuration lock. Only the resulting local node is persisted. */
internal class BoxWarp(
    private val prepare: suspend (Long, String, Long) -> WarpProfileSnapshot,
    private val commit: suspend (WarpProfileSnapshot, String) -> Map<String, Any?>,
    private val registerNode: suspend (WarpProfileSnapshot, String, String, (String) -> Unit) -> Unit = ::registerWarpNode,
    private val localSession: () -> Long = CommandTarget::localSession,
) : Closeable {
    private class Pending(val id: String, val profileId: Long, val path: String, val name: String, val transport: String, val node: String) {
        fun toWire(): Map<String, Any?> = mapOf("pendingId" to id, "profileId" to profileId, "name" to name, "transport" to transport)
        fun failure() = BoxWarpException("warp_pending",
            "WARP registered, but the configuration could not be saved. Select the original profile and retry saving.", toWire())
    }
    private class Active(val id: String, val job: Job)
    private val access = Any()
    private var active: Active? = null
    // Credentials are deliberately confined to this host's memory until a confirmed save.
    private var pending: Pending? = null
    private var closed = false

    fun snapshot(): Map<String, Any?> = synchronized(access) {
        mapOf("registering" to (active != null), "pending" to pending?.toWire())
    }

    private fun begin(id: String, job: Job, retry: Boolean = false): Active = synchronized(access) {
        if (closed) throw BoxWarpException("warp_error", "WARP registration is no longer available")
        if (active != null) throw BoxWarpException("warp_busy", "A WARP operation is already in progress")
        if (!retry) pending?.let { throw it.failure() }
        Active(id, job).also { active = it }
    }

    private fun finish(operation: Active) = synchronized(access) {
        if (active === operation) active = null
    }

    suspend fun register(requestId: String, profileId: Long, revision: String, name: String, transport: String): Map<String, Any?> = coroutineScope {
        if (requestId.isBlank() || requestId.length > 128) throw BoxWarpException("warp_error", "Invalid WARP request")
        val label = name.trim()
        if (label.isEmpty() || label.length > 128 || label.any { it.isISOControl() }) throw BoxWarpException("warp_error", "Invalid WARP node name")
        if (transport !in setOf("wireguard", "http2", "http3")) {
            throw BoxWarpException("warp_error", "Select WireGuard, HTTP/2 or HTTP/3 for WARP")
        }
        val operation = begin(requestId, coroutineContext.job)
        try {
            val target = prepare(profileId, revision, localSession())
            var registeredRecord: Pending? = null
            try {
                registerNode(target, label, transport) { node ->
                    val record = Pending(UUID.randomUUID().toString(), target.profileId, target.path, label, transport, node)
                    synchronized(access) {
                        if (!closed) { pending = record; registeredRecord = record }
                    }
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                synchronized(access) { registeredRecord }?.let { throw it.failure() }
                // Never forward a response body, token, private key or arbitrary native error.
                throw BoxWarpException("warp_error", "WARP registration failed. Check connectivity and retry.")
            }
            val record = synchronized(access) { registeredRecord }
                ?: throw BoxWarpException("warp_error", "WARP registration did not return a node")
            save(target, record)
        } finally {
            finish(operation)
        }
    }

    suspend fun retry(pendingId: String, profileId: Long, revision: String): Map<String, Any?> = coroutineScope {
        val operation = begin(pendingId, coroutineContext.job, retry = true)
        try {
            val record = synchronized(access) { pending?.takeIf { it.id == pendingId } }
                ?: throw BoxWarpException("warp_error", "The registered WARP node is no longer available")
            if (profileId != record.profileId) throw record.failure()
            val target = try {
                prepare(profileId, revision, localSession())
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                throw record.failure()
            }
            if (target.path != record.path) throw record.failure()
            save(target, record)
        } finally {
            finish(operation)
        }
    }

    private suspend fun save(target: WarpProfileSnapshot, record: Pending): Map<String, Any?> {
        try {
            currentCoroutineContext().ensureActive()
            val result = commit(target, record.node)
            synchronized(access) { if (pending === record) pending = null }
            return result
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            throw record.failure()
        }
    }

    fun discard(pendingId: String) = synchronized(access) {
        if (active != null) throw BoxWarpException("warp_busy", "Wait for the WARP operation to finish")
        if (pending?.id != pendingId) throw BoxWarpException("warp_error", "The registered WARP node is no longer available")
        pending = null
    }

    fun cancel(requestId: String): Boolean {
        val job = synchronized(access) { active?.takeIf { it.id == requestId }?.job } ?: return false
        job.cancel(CancellationException("WARP registration cancelled"))
        return true
    }

    override fun close() {
        val job = synchronized(access) { closed = true; pending = null; active?.job }
        job?.cancel()
    }
}

/** The JNI request receives cancellation immediately, while its IO coroutine is blocked. */
private suspend fun registerWarpNode(target: WarpProfileSnapshot, name: String, transport: String, registered: (String) -> Unit): Unit = coroutineScope {
    val owner = coroutineContext
    var task: UIWARPRegistrationTask? = null
    try {
        withContext(Dispatchers.IO) {
            owner.ensureActive()
            CommandTarget.requireLocalSession(target.session)
            task = when (BoxService.currentStatus) {
                Status.Started -> (BoxService.dashboardServer ?: error("The local service is not available")).newUIWARPRegistrationTask()
                Status.Stopped -> {
                    check(BoxService.dashboardServer == null) { "The local service is changing" }
                    Libbox.newUIWARPRegistrationTask()
                }
                else -> error("The local service is changing")
            }
            // Keep the holder even if withContext cancels instead of returning it.
            owner.ensureActive()
        }
        val request = checkNotNull(task)
        withWarpRegistrationTask(request::close, registered) {
            when (transport) {
                "wireguard" -> request.registerWireGuard(name)
                "http2" -> request.register(name, 2)
                "http3" -> request.register(name, 3)
                else -> error("Invalid WARP transport")
            }
        }
    } finally {
        task?.close()
    }
    Unit
}

internal suspend fun <T> withWarpRegistrationTask(close: () -> Unit, onResult: (T) -> Unit = {}, register: () -> T): T = coroutineScope {
    val owner = coroutineContext
    val cancellation = launch(Dispatchers.Unconfined, start = CoroutineStart.UNDISPATCHED) {
        try { awaitCancellation() } finally { close() }
    }
    try {
        withContext(Dispatchers.IO) {
            owner.ensureActive()
            val node = try { register() } catch (error: Exception) {
                owner.ensureActive()
                throw error
            }
            // Publish credentials before returning through withContext's cancellation check.
            onResult(node)
            owner.ensureActive()
            node
        }
    } finally {
        close()
        cancellation.cancel()
    }
}

/** Uses the same validated, atomic write and confirmed service recovery as node editing. */
internal class BoxWarpProfiles(
    private val beforeApply: suspend () -> Unit = { ProfileContent.requireStableService() },
    private val applySelected: suspend (recover: Boolean) -> Unit,
) {
    private fun requireSession(session: Long) {
        try { CommandTarget.requireLocalSession(session) } catch (_: Exception) {
            throw BoxWarpException("warp_error", "Exit remote control and retry the local WARP operation")
        }
    }

    private suspend fun stable() {
        try { beforeApply() } catch (cancelled: CancellationException) { throw cancelled } catch (_: Exception) {
            throw BoxWarpException("warp_error", "Wait for the service to finish starting or stopping")
        }
    }

    private suspend fun selected(id: Long, revision: String, path: String? = null): Pair<Profile, String> {
        if (id != Settings.selectedProfile) throw BoxWarpException("warp_error", "Selected profile changed; select the original profile and retry")
        val profile = ProfileManager.get(id) ?: throw BoxWarpException("warp_error", "Select a profile before registering WARP")
        if (path != null && path != profile.typed.path) throw BoxWarpException("warp_error", "The target profile changed; reload it before saving")
        val content = try { File(profile.typed.path).readText() } catch (_: Exception) {
            throw BoxWarpException("warp_error", "The selected profile could not be read")
        }
        if (revision.isEmpty() || ProfileContent.revision(content) != revision) throw BoxWarpException("warp_error", "Configuration changed; reload it before saving")
        return profile to content
    }

    suspend fun prepare(id: Long, revision: String, session: Long): WarpProfileSnapshot = ProfileContent.mutex.withLock {
        requireSession(session)
        stable()
        val (profile, _) = selected(id, revision)
        WarpProfileSnapshot(id, revision, session, profile.typed.path)
    }

    suspend fun commit(target: WarpProfileSnapshot, node: String): Map<String, Any?> = ProfileContent.mutex.withLock {
        requireSession(target.session)
        stable()
        val (profile, content) = selected(target.profileId, target.revision, target.path)
        val registered = JSONObject(node)
        val update = Libbox.addUIConfigNodes(content, JSONArray().put(registered).toString(), profile.typed.isDefault)
        val type = registered.getString("type")
        val section = when (type) {
            "wireguard" -> "endpoints"
            "masque" -> "outbounds"
            else -> error("Unsupported registered WARP node")
        }
        val nodes = JSONObject(Libbox.formatConfig(update.config).value).optJSONArray(section) ?: JSONArray()
        val tag = (0 until nodes.length()).asSequence().map { nodes.getJSONObject(it) }.firstOrNull {
            it.optString("type") == type && it.optString("private_key") == registered.getString("private_key")
        }?.getString("tag") ?: error("Registered WARP node was not added")
        ProfileContent.replaceLocked(profile, update.config, expectedRevision = target.revision,
            beforeCommit = {
                requireSession(target.session)
                if (Settings.selectedProfile != target.profileId) throw BoxWarpException("warp_error", "Selected profile changed; reload it before saving")
                check(ProfileContent.revision(File(target.path).readText()) == target.revision) { "Configuration changed; reload it before saving" }
            },
            beforeSave = { requireSession(target.session); stable() }, applySelected = applySelected)
        mapOf("profileId" to target.profileId, "tag" to tag, "added" to update.added, "skipped" to update.skipped)
    }
}
