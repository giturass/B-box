package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.bg.UpdateProfileWork
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Profile
import io.nekohasekai.sfa.database.ProfileChange
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.ProfileSubscription
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.TypedProfile
import io.nekohasekai.sfa.utils.CommandTarget
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.sync.withLock
import org.json.JSONObject
import java.io.File
import java.net.URI
import java.util.Date
import java.util.UUID

internal class BoxProfiles(
    private val beforeApply: suspend () -> Unit = { ProfileContent.requireStableService() },
    private val download: suspend (String) -> String = HTTPClient::getString,
    private val applySelected: suspend (recover: Boolean) -> Unit,
) {
    private val mutex = ProfileContent.mutex

    suspend fun get(id: Long): Profile = ProfileManager.get(id) ?: error("Profile no longer exists")

    suspend fun list(): List<Map<String, Any>> = ProfileManager.list().map { it.toWire() }

    suspend fun read(id: Long): String = mutex.withLock { File(get(id).typed.path).readText() }

    suspend fun config(id: Long): Map<String, Any?> = mutex.withLock { configLocked(id) }

    private suspend fun configLocked(id: Long): Map<String, Any?> {
        val content = File(get(id).typed.path).readText()
        val formatted = Libbox.formatConfig(content).value
        val root = JSONObject(formatted)
        val outbounds = root.optJSONArray("outbounds")
        var groupCount = 0
        for (index in 0 until (outbounds?.length() ?: 0)) {
            if (outbounds?.optJSONObject(index)?.optString("type") in setOf("selector", "urltest")) groupCount++
        }
        val route = root.optJSONObject("route")
        return mapOf("content" to content, "formatted" to formatted, "revision" to revision(content), "stats" to mapOf(
            "groups" to groupCount, "outbounds" to (outbounds?.length() ?: 0),
            "rules" to (route?.optJSONArray("rules")?.length() ?: 0),
            "ruleSets" to (route?.optJSONArray("rule_set")?.length() ?: 0),
            "dnsServers" to (root.optJSONObject("dns")?.optJSONArray("servers")?.length() ?: 0),
            "endpoints" to (root.optJSONArray("endpoints")?.length() ?: 0),
        ))
    }

    suspend fun stats(id: Long): Any? = mutex.withLock {
        if (File(get(id).typed.path).length() > 8L * 1024 * 1024) null else configLocked(id)["stats"]
    }

    suspend fun writeContent(id: Long, content: String, expectedRevision: String): Map<String, Any?> = mutex.withLock {
        writeContentLocked(id, content, expectedRevision)
    }

    suspend fun setLanProxy(id: Long, expectedRevision: String, enabled: Boolean, initialPort: Int): Map<String, Any?> = mutex.withLock {
        require(!CommandTarget.isRemote) { "Disconnect remote session to edit local profiles" }
        require(id == Settings.selectedProfile) { "Selected profile changed; reload it before saving" }
        val previous = File(get(id).typed.path).readText()
        require(revision(previous) == expectedRevision) { "Configuration changed; reload it before saving" }
        val content = Libbox.setUILANProxyEnabled(previous, enabled, initialPort).value
        val saved = writeContentLocked(id, content, expectedRevision)
        if (previous == content && id == Settings.selectedProfile) applySelected(false)
        saved
    }

    private suspend fun writeContentLocked(id: Long, content: String, expectedRevision: String): Map<String, Any?> {
        ProfileContent.replaceLocked(get(id), content, expectedRevision = expectedRevision, beforeSave = beforeApply, applySelected = applySelected)
        return configLocked(id)
    }

    suspend fun select(id: Long, serviceStatus: () -> Status) = mutex.withLock {
        get(id)
        ProfileContent.requireStableService(serviceStatus())
        val previous = Settings.selectedProfile
        val previousMode = Settings.serviceMode
        ProfileChange.apply(
            save = {
                ProfileContent.snapshotMutex.withLock {
                    ProfileContent.requireStableService(serviceStatus())
                    Settings.saveProfileSelection(id)
                }
            },
            restore = listOf<suspend () -> Unit>({
                Settings.saveProfileSelection(previous, previousMode)
            }),
            restoreBarrier = { work -> ProfileContent.snapshotMutex.withLock { work() } },
            applyService = applySelected,
        )
    }

    internal data class PreparedSave(
        val id: Long, val name: String, val url: String, val interval: Int, val autoUpdate: Boolean,
        val content: String?, val updatedAt: Long?, val expectedRevision: String?,
    )

    /** URL imports download before entering either the bridge or configuration operation lock. */
    suspend fun prepareSave(args: Map<String, Any?>): PreparedSave {
        val id = (args["id"] as? Number)?.toLong() ?: 0L
        require(id >= 0L) { "Invalid profile ID" }
        val name = (args["name"] as? String)?.trim().orEmpty()
        require(name.isNotEmpty()) { "A profile name is required" }
        val url = (args["url"] as? String)?.trim().orEmpty()
        if (url.isNotEmpty()) validateUrl(url)
        val interval = (args["interval"] as? Number)?.toInt() ?: 60
        require(interval >= 15) { "Update interval must be at least 15 minutes" }
        val content = args["content"] as? String ?: if (id == 0L) {
            if (url.isNotEmpty()) download(url) else "{}"
        } else null
        content?.let { validate(Libbox.removeUIRetiredCaptureOptions(it)) }
        return PreparedSave(id, name, url, interval, url.isNotEmpty() && args["autoUpdate"] == true,
            content, (args["updatedAt"] as? Number)?.toLong()?.takeIf { it >= 0L && args["content"] is String },
            args["revision"] as? String)
    }

    suspend fun save(args: Map<String, Any?>): Long = savePrepared(prepareSave(args))

    suspend fun savePrepared(request: PreparedSave, beforeCommit: () -> Unit = {}): Long = mutex.withLock {
        beforeCommit()
        val id = request.id
        val previousProfile = if (id == 0L) null else get(id)
        val file = if (id == 0L) File(Application.application.filesDir, "configs/${UUID.randomUUID()}.json")
            else File(previousProfile!!.typed.path)
        val oldContent = if (file.exists()) file.readText() else null
        val content = Libbox.removeUIRetiredCaptureOptions(request.content ?: oldContent ?: error("Profile file is missing"))
        validate(content)
        val changed = content != oldContent
        if (id != 0L && request.content != null && changed) {
            require(oldContent != null && request.expectedRevision == revision(oldContent)) { "Configuration changed; reload it before saving" }
        }
        val oldSelection = Settings.selectedProfile
        val oldMode = Settings.serviceMode
        val select = id == 0L && oldSelection == -1L
        val apply = changed && (id == oldSelection || select)
        if (apply) beforeApply()
        val profile = Profile(id, previousProfile?.userOrder ?: ProfileManager.nextOrder(), request.name, previousProfile?.icon,
            TypedProfile().apply {
                path = file.path
                type = if (request.url.isEmpty()) TypedProfile.Type.Local else TypedProfile.Type.Remote
                remoteURL = request.url
                autoUpdate = request.autoUpdate
                autoUpdateInterval = request.interval
                lastUpdated = when {
                    id == 0L -> Date(if (request.url.isNotEmpty()) request.updatedAt ?: System.currentTimeMillis() else System.currentTimeMillis())
                    changed && type == TypedProfile.Type.Local -> Date()
                    else -> Date(previousProfile!!.typed.lastUpdated.time)
                }
            })
        val restore = listOf<suspend () -> Unit>(
            { if (oldContent != null) write(file, oldContent) else check(!file.exists() || file.delete()) { "Unable to remove profile file" } },
            { if (previousProfile != null) check(ProfileManager.update(previousProfile) == 1) else if (profile.id != 0L) ProfileManager.delete(profile) },
            { if (apply) Settings.saveProfileSelection(oldSelection, oldMode) },
        )
        ProfileChange.apply(
            save = {
                ProfileContent.snapshotMutex.withLock {
                    beforeCommit()
                    if (apply) beforeApply()
                    check(file.parentFile!!.isDirectory || file.parentFile!!.mkdirs()) { "Unable to create configuration directory" }
                    if (changed) write(file, content)
                    if (id == 0L) ProfileManager.create(profile) else check(ProfileManager.update(profile) == 1) { "Profile no longer exists" }
                    if (select) Settings.saveProfileSelection(profile.id)
                }
            },
            restore = restore,
            restoreBarrier = { work -> ProfileContent.snapshotMutex.withLock { work() } },
            applyService = { recover -> if (apply) applySelected(recover) },
        )
        UpdateProfileWork.reconfigureUpdater()
        profile.id
    }

    suspend fun prepareUpdate(id: Long): ProfileSubscription = mutex.withLock {
        ProfileSubscription.captureLocked(get(id)).also { validateUrl(it.url) }
    }

    suspend fun downloadUpdate(snapshot: ProfileSubscription): String = download(snapshot.url)

    suspend fun commitUpdate(snapshot: ProfileSubscription, content: String, beforeCommit: () -> Unit = {}) = mutex.withLock {
        beforeCommit()
        val profile = snapshot.currentLocked()
        ProfileContent.replaceLocked(profile, content, updatedAt = Date(), beforeSave = { beforeCommit(); beforeApply() }, applySelected = applySelected)
    }

    suspend fun update(id: Long) {
        val snapshot = prepareUpdate(id)
        val content = downloadUpdate(snapshot)
        commitUpdate(snapshot, content)
    }

    suspend fun delete(id: Long) = mutex.withLock {
        require(id != Settings.selectedProfile) { "Select another profile before deleting this one" }
        val profile = get(id)
        ProfileManager.delete(profile)
        File(profile.typed.path).delete()
        UpdateProfileWork.reconfigureUpdater()
    }

    suspend fun reorder(ids: List<Long>) = mutex.withLock {
        val profiles = ProfileManager.list()
        require(ids.size == profiles.size && ids.toSet() == profiles.map { it.id }.toSet()) { "Profile list changed; refresh and retry" }
        val byId = profiles.associateBy { it.id }
        ProfileManager.update(ids.mapIndexed { index, id -> byId.getValue(id).apply { userOrder = index.toLong() } })
    }

    fun decode(data: ByteArray, name: String): Map<String, Any?> {
        val text = data.toString(Charsets.UTF_8)
        if (text.trimStart().startsWith("{")) {
            validate(text)
            return mapOf("name" to name.substringBeforeLast('.'), "content" to text)
        }
        val profile = Libbox.decodeProfileContent(data)
        validate(profile.config)
        require(profile.type == Libbox.ProfileTypeLocal || profile.type == Libbox.ProfileTypeRemote) { "Unsupported profile type" }
        val remote = profile.type == Libbox.ProfileTypeRemote
        val url = if (remote) validateUrl(profile.remotePath) else ""
        return mapOf("name" to profile.name, "content" to profile.config,
            "remote" to remote, "url" to url, "autoUpdate" to (remote && profile.autoUpdate),
            "interval" to if (remote) profile.autoUpdateInterval.coerceAtLeast(15) else 60,
            "updatedAt" to if (remote) profile.lastUpdated.coerceAtLeast(0) else 0L)
    }

    companion object {
        fun revision(content: String): String = ProfileContent.revision(content)
        fun validate(content: String) = ProfileContent.validate(content)

        fun validateUrl(value: String): String {
            val uri = URI(value)
            require(uri.scheme in listOf("https", "http") && !uri.host.isNullOrBlank()) { "Enter an HTTP or HTTPS URL serving sing-box JSON" }
            return value
        }

        fun write(file: File, content: String) = ProfileContent.write(file, content)
    }
}

internal fun Profile.toWire(): Map<String, Any> = mapOf(
    "id" to id,
    "name" to name,
    "remote" to (typed.type == TypedProfile.Type.Remote),
    "url" to typed.remoteURL,
    "updatedAt" to typed.lastUpdated.time,
    "autoUpdate" to typed.autoUpdate,
    "interval" to typed.autoUpdateInterval,
)
