package io.nekohasekai.sfa.flutter

import android.util.AtomicFile
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.bg.UpdateProfileWork
import io.nekohasekai.sfa.database.Profile
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.TypedProfile
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.io.File
import java.net.URI
import java.util.Date
import java.util.UUID

internal class BoxProfiles(private val applySelected: suspend () -> Unit) {
    private val mutex = Mutex()

    suspend fun get(id: Long): Profile = ProfileManager.get(id) ?: error("Profile no longer exists")

    suspend fun list(): List<Map<String, Any>> = ProfileManager.list().map { it.toWire() }

    suspend fun read(id: Long): String = File(get(id).typed.path).readText()

    suspend fun select(id: Long) = mutex.withLock {
        get(id)
        Settings.selectedProfile = id
        Settings.dataStore.flush()
        applySelected()
    }

    suspend fun save(args: Map<String, Any?>): Long = mutex.withLock {
        val id = (args["id"] as? Number)?.toLong() ?: 0L
        val profile = if (id == 0L) Profile() else get(id)
        val name = (args["name"] as? String)?.trim().orEmpty()
        require(name.isNotEmpty()) { "A profile name is required" }
        val url = (args["url"] as? String)?.trim().orEmpty()
        if (url.isNotEmpty()) validateUrl(url)
        val interval = (args["interval"] as? Number)?.toInt() ?: 60
        require(interval >= 15) { "Update interval must be at least 15 minutes" }
        val content = args["content"] as? String ?: if (id == 0L && url.isNotEmpty()) {
            HTTPClient().use { it.getString(url) }
        } else if (id != 0L) {
            File(profile.typed.path).readText()
        } else {
            "{}"
        }
        validate(content)
        val file = if (id == 0L) {
            File(Application.application.filesDir, "configs/${UUID.randomUUID()}.json").also { it.parentFile!!.mkdirs() }
        } else {
            File(profile.typed.path)
        }
        val oldContent = if (file.exists()) file.readText() else null
        write(file, content)
        profile.name = name
        profile.typed.path = file.path
        profile.typed.type = if (url.isEmpty()) TypedProfile.Type.Local else TypedProfile.Type.Remote
        profile.typed.remoteURL = url
        profile.typed.autoUpdate = url.isNotEmpty() && args["autoUpdate"] == true
        profile.typed.autoUpdateInterval = interval
        if (id == 0L && url.isNotEmpty()) profile.typed.lastUpdated = Date()
        try {
            if (id == 0L) {
                profile.userOrder = ProfileManager.nextOrder()
                ProfileManager.create(profile, andSelect = Settings.selectedProfile == -1L)
            } else {
                ProfileManager.update(profile)
            }
        } catch (e: Exception) {
            if (oldContent != null) write(file, oldContent) else file.delete()
            throw e
        }
        UpdateProfileWork.reconfigureUpdater()
        if (profile.id == Settings.selectedProfile && content != oldContent) applySelected()
        profile.id
    }

    suspend fun update(id: Long) = mutex.withLock {
        val profile = get(id)
        require(profile.typed.type == TypedProfile.Type.Remote) { "Only remote profiles can be updated" }
        val content = HTTPClient().use { it.getString(validateUrl(profile.typed.remoteURL)) }
        validate(content)
        val file = File(profile.typed.path)
        val changed = !file.exists() || file.readText() != content
        if (changed) write(file, content)
        profile.typed.lastUpdated = Date()
        ProfileManager.update(profile)
        if (changed && id == Settings.selectedProfile) applySelected()
    }

    suspend fun delete(id: Long) = mutex.withLock {
        val profile = get(id)
        ProfileManager.delete(profile)
        File(profile.typed.path).delete()
        if (Settings.selectedProfile == id) {
            Settings.selectedProfile = ProfileManager.list().firstOrNull()?.id ?: -1L
            Settings.dataStore.flush()
            applySelected()
        }
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
        return mapOf("name" to profile.name, "content" to profile.config)
    }

    companion object {
        fun validate(content: String) {
            require(content.toByteArray().size <= 16 * 1024 * 1024) { "Configuration exceeds 16 MiB" }
            Libbox.checkConfig(content)
        }

        fun validateUrl(value: String): String {
            val uri = URI(value)
            require(uri.scheme in listOf("https", "http") && !uri.host.isNullOrBlank()) { "Enter an HTTP or HTTPS URL serving sing-box JSON" }
            return value
        }

        fun write(file: File, content: String) {
            val atomic = AtomicFile(file)
            val stream = atomic.startWrite()
            try {
                stream.write(content.toByteArray())
                atomic.finishWrite(stream)
            } catch (e: Exception) {
                atomic.failWrite(stream)
                throw e
            }
        }
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
