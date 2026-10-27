package io.nekohasekai.sfa.flutter

import android.util.AtomicFile
import io.nekohasekai.sfa.bg.UpdateProfileWork
import io.nekohasekai.sfa.database.Profile
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.RemoteServer
import io.nekohasekai.sfa.database.RemoteServerManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.TypedProfile
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.util.Date
import java.util.UUID

internal class BoxBackups(
    private val activity: BoxActivity,
    private val readSettings: () -> Map<String, Any?>,
    private val writeSettings: suspend (Map<String, Any?>) -> Unit,
    private val applySelected: suspend (Boolean) -> Unit,
    private val publish: suspend () -> Unit,
    private val emit: (String, Any?) -> Unit,
    private val webdav: BoxWebDAV,
) {
    private val preferences = activity.getSharedPreferences("flclash_ui", 0)
    private val terminal = BoxTerminalSettings(activity)

    private fun origin(): String = preferences.getString("backupOrigin", null) ?: UUID.randomUUID().toString().also {
        check(preferences.edit().putString("backupOrigin", it).commit()) { "Unable to save backup identity" }
    }
    private fun profileKey(profile: Profile, origin: String): String {
        val name = File(profile.typed.path).nameWithoutExtension
        return runCatching { UUID.fromString(name).toString().takeIf { it == name } }.getOrNull()
            ?: UUID.nameUUIDFromBytes("$origin:${profile.id}".toByteArray()).toString()
    }
    private fun readPreferences() = BoxPreferences.validate(jsonObject(JSONObject(preferences.getString("value", "{}")!!)))
    private fun editableSettings() = readSettings().filterKeys { it !in setOf("batteryExempt", "perAppManagedList") }

    suspend fun create(): Map<String, Any?> = ProfileContent.mutex.withLock {
        val origin = origin()
        var size = 0L
        val profiles = ProfileManager.list().map { profile ->
            val file = File(profile.typed.path)
            size += file.length()
            require(size <= MAX_BYTES) { "Backup exceeds 64 MiB" }
            val content = file.readText()
            BoxProfiles.validate(content)
            profile.toWire() + mapOf("key" to profileKey(profile, origin), "content" to content)
        }
        val backup = mapOf("format" to "bbox-1", "origin" to origin, "createdAt" to System.currentTimeMillis(),
            "selectedProfile" to Settings.selectedProfile, "profiles" to profiles,
            "preferences" to readPreferences(), "settings" to editableSettings(),
            "terminal" to terminal.backupSnapshot(), "webdav" to webdav.snapshot(),
            "servers" to RemoteServerManager.list().map { mapOf("name" to it.name, "url" to it.url, "secret" to it.secret) })
        encode(backup)
        backup
    }

    fun encode(backup: Map<String, Any?>): ByteArray = JSONObject(backup).toString().toByteArray().also {
        require(it.size <= MAX_BYTES) { "Backup exceeds 64 MiB" }
    }

    suspend fun read(bytes: ByteArray, scope: String): Map<String, Any?> {
        require(bytes.size <= MAX_BYTES) { "Backup exceeds 64 MiB" }
        val backup = jsonObject(JSONObject(bytes.toString(Charsets.UTF_8)))
        validate(backup, scope)
        return backup
    }

    @Suppress("UNCHECKED_CAST")
    private suspend fun validate(backup: Map<String, Any?>, scope: String): List<Map<String, Any?>> {
        require(scope in setOf("all", "profiles")) { "Invalid restore scope" }
        require(backup["format"] == "bbox-1") { "Unsupported B-box backup format" }
        backup["origin"]?.let { require(validUUID(it)) { "Invalid backup origin" } }
        backup["createdAt"]?.let { require(integer(it, 0)) { "Invalid backup timestamp" } }
        val entries = rows(backup["profiles"] ?: error("Backup has no profiles"))
        require(entries.size <= 1000) { "Backup contains too many profiles" }
        entries.forEach { entry ->
            require((entry["name"] as? String)?.let { it.isNotBlank() && it.length <= 4096 } == true) { "Profile name is required" }
            BoxProfiles.validate(entry["content"] as? String ?: error("Missing profile content"))
            require(entry["url"] == null || entry["url"] is String) { "Invalid profile URL" }
            val url = entry["url"] as? String ?: ""
            if (url.isNotEmpty()) BoxProfiles.validateUrl(url)
            entry["key"]?.let { require(validUUID(it)) { "Invalid profile backup identity" } }
            entry["id"]?.let { require(integer(it, 1)) { "Invalid profile ID" } }
            entry["interval"]?.let { require(it is Number && it.toDouble() == it.toInt().toDouble() && it.toInt() >= 15) { "Invalid update interval" } }
            entry["autoUpdate"]?.let { require(it is Boolean) { "Invalid auto update option" } }
            entry["updatedAt"]?.let { require(integer(it, 0)) { "Invalid update timestamp" } }
        }
        val keys = entries.mapNotNull { it["key"] }
        val ids = entries.mapNotNull { (it["id"] as? Number)?.toLong() }
        require(keys.distinct().size == keys.size && ids.distinct().size == ids.size) { "Backup contains duplicate profiles" }
        backup["selectedProfile"]?.let {
            require(integer(it, -1) && ((it as Number).toLong() == -1L || it.toLong() in ids)) { "Invalid selected profile" }
        }
        encode(backup)
        if (scope == "all") {
            BoxPreferences.validate(objectValue(backup, "preferences") ?: emptyMap())
            BoxSettings.validate(objectValue(backup, "settings") ?: emptyMap())
            val servers = rows(backup["servers"])
            require(servers.size <= 1000) { "Too many remote servers" }
            servers.forEach {
                require(it["name"] is String && (it["url"] as? String)?.let(RemoteServer::validateURL) != null) { "Invalid remote server" }
                require(it["secret"] == null || it["secret"] is String) { "Invalid remote secret" }
            }
            val identities = servers.map { RemoteServer.validateURL(it["url"] as String) to it["name"] }
            require(identities.distinct().size == identities.size) { "Backup contains duplicate remote servers" }
            objectValue(backup, "terminal")?.let { terminal.validateBackup(it) }
            objectValue(backup, "webdav")?.let(BoxWebDAV::validate)
        }
        return entries
    }

    @Suppress("UNCHECKED_CAST")
    suspend fun restore(backup: Map<String, Any?>, scope: String, strategy: String): Int {
        require(strategy in setOf("Compatible", "Override")) { "Invalid restore strategy" }
        val entries = validate(backup, scope)
        val count = ProfileContent.mutex.withLock {
            val oldProfiles = ProfileManager.list()
            val oldServers = RemoteServerManager.list()
            val oldSelection = Settings.selectedProfile
            val oldPreferences = preferences.getString("value", "{}")!!
            val oldSettings = editableSettings()
            val oldTerminal = terminal.snapshot()
            val oldWebDAV = webdav.snapshot()
            val workDir = File(activity.cacheDir, "restore-${UUID.randomUUID()}")
            check(workDir.mkdirs()) { "Unable to stage restore" }
            val originals = mutableMapOf<File, File?>()
            var newFont: File? = null
            var cleanup = true
            fun keepOriginal(file: File) {
                if (originals.containsKey(file)) return
                originals[file] = if (file.exists()) file.copyTo(File(workDir, originals.size.toString())) else null
            }
            try {
                val mapping = mutableMapOf<Long, Long>()
                val restored = mutableListOf<Profile>()
                val byKey = oldProfiles.associateBy { profileKey(it, origin()) }
                var nextOrder = (oldProfiles.maxOfOrNull { it.userOrder } ?: -1L) + 1
                ProfileManager.transaction {
                    entries.forEachIndexed { index, entry ->
                        val key = entry["key"] as? String
                        val existing = key?.let(byKey::get) ?: if (backup["origin"] == origin()) oldProfiles.firstOrNull { it.id == (entry["id"] as? Number)?.toLong() } else null
                        val profile = existing?.let(::copyProfile) ?: Profile()
                        val file = existing?.let { File(it.typed.path) } ?: File(File(activity.filesDir, "configs").apply { mkdirs() }, "${key ?: UUID.randomUUID()}.json")
                        keepOriginal(file)
                        ProfileContent.write(file, entry["content"] as String)
                        profile.name = (entry["name"] as String).trim()
                        profile.typed.path = file.path
                        profile.typed.remoteURL = entry["url"] as? String ?: ""
                        profile.typed.type = if (profile.typed.remoteURL.isEmpty()) TypedProfile.Type.Local else TypedProfile.Type.Remote
                        profile.typed.autoUpdate = profile.typed.remoteURL.isNotEmpty() && entry["autoUpdate"] == true
                        profile.typed.autoUpdateInterval = (entry["interval"] as? Number)?.toInt() ?: 60
                        profile.typed.lastUpdated = Date((entry["updatedAt"] as? Number)?.toLong() ?: 0L)
                        profile.userOrder = if (strategy == "Override") index.toLong() else existing?.userOrder ?: nextOrder++
                        if (existing == null) ProfileManager.create(profile) else ProfileManager.update(profile)
                        restored.add(profile)
                        (entry["id"] as? Number)?.toLong()?.let { mapping[it] = profile.id }
                    }
                    if (strategy == "Override") ProfileManager.delete(oldProfiles.filter { old -> restored.none { it.id == old.id } })
                    if (scope == "all") {
                        val keptServers = mutableSetOf<Long>()
                        for ((index, entry) in rows(backup["servers"]).withIndex()) {
                            val url = RemoteServer.validateURL(entry["url"] as String)!!
                            val existing = oldServers.firstOrNull { RemoteServer.normalizeURL(it.url) == url && it.name == entry["name"] }
                            val server = RemoteServer(existing?.id ?: 0L, existing?.userOrder ?: 0L,
                                entry["name"] as String, url, entry["secret"] as? String ?: "")
                            if (existing == null) RemoteServerManager.create(server) else RemoteServerManager.update(server)
                            if (strategy == "Override") {
                                server.userOrder = index.toLong()
                                RemoteServerManager.update(server)
                            }
                            keptServers.add(server.id)
                        }
                        if (strategy == "Override") oldServers.filter { it.id !in keptServers }.forEach { RemoteServerManager.delete(it) }
                    }
                }
                val current = ProfileManager.list()
                val backupSelection = (backup["selectedProfile"] as? Number)?.toLong()
                Settings.selectedProfile = if (scope == "all" && backupSelection == -1L) -1L else
                    (if (scope == "all") mapping[backupSelection] else null)
                        ?: oldSelection.takeIf { selection -> current.any { it.id == selection } }
                        ?: restored.firstOrNull()?.id ?: -1L
                if (scope == "all") {
                    writeSettings(backup["settings"] as? Map<String, Any?> ?: emptyMap())
                    (backup["terminal"] as? Map<String, Any?>)?.let { newFont = terminal.restoreBackup(it) }
                    if (backup.containsKey("webdav")) webdav.save(objectValue(backup, "webdav"))
                    val restoredPreferences = (backup["preferences"] as? Map<String, Any?> ?: emptyMap()).toMutableMap()
                    val folds = restoredPreferences["proxyUnfolded"] as? Map<String, Any?>
                    if (folds != null) restoredPreferences["proxyUnfolded"] = folds.entries.associate { (key, value) ->
                        val id = key.removePrefix("local:").toLongOrNull()
                        (if (key.startsWith("local:") && id != null && mapping.containsKey(id)) "local:${mapping[id]}" else key) to value
                    }
                    check(preferences.edit().putString("value", JSONObject(restoredPreferences).toString()).commit()) { "Unable to restore preferences" }
                }
                Settings.dataStore.flush()
                applySelected(scope == "all")
                val keptPaths = current.map { it.typed.path }.toSet()
                publish()
                emit("preferences", readPreferences())
                if (strategy == "Override") oldProfiles.filter { it.typed.path !in keptPaths }.forEach { runCatching { File(it.typed.path).delete() } }
                restored.size
            } catch (error: Exception) {
                withContext(NonCancellable) {
                    suspend fun attempt(task: suspend () -> Unit) {
                        try { task() } catch (rollback: Exception) { cleanup = false; error.addSuppressed(rollback) }
                    }
                    attempt {
                        ProfileManager.transaction {
                            ProfileManager.delete(ProfileManager.list().filter { now -> oldProfiles.none { it.id == now.id } })
                            for (profile in oldProfiles) if (ProfileManager.get(profile.id) == null) ProfileManager.create(profile) else ProfileManager.update(profile)
                            if (scope == "all") {
                                RemoteServerManager.list().filter { now -> oldServers.none { it.id == now.id } }.forEach { RemoteServerManager.delete(it) }
                                for (server in oldServers) if (RemoteServerManager.get(server.id) == null) {
                                    val copy = RemoteServer(server.id, server.userOrder, server.name, server.url, server.secret)
                                    RemoteServerManager.create(copy)
                                    copy.userOrder = server.userOrder
                                    RemoteServerManager.update(copy)
                                } else RemoteServerManager.update(server)
                            }
                        }
                    }
                    for ((file, original) in originals) attempt {
                        if (original == null) { if (file.exists()) check(file.delete()) } else restoreFile(file, original)
                    }
                    Settings.selectedProfile = oldSelection
                    if (scope == "all") {
                        attempt { writeSettings(oldSettings) }
                        attempt { terminal.restoreState(oldTerminal) }
                        attempt { webdav.save(oldWebDAV) }
                        attempt { check(preferences.edit().putString("value", oldPreferences).commit()) }
                    }
                    newFont?.delete()
                    attempt { Settings.dataStore.flush() }
                    attempt { applySelected(true) }
                    attempt { publish(); emit("preferences", readPreferences()) }
                }
                throw error
            } finally { if (cleanup) workDir.deleteRecursively() }
        }
        UpdateProfileWork.reconfigureUpdater()
        return count
    }

    companion object {
        const val MAX_BYTES = 64 * 1024 * 1024
        private fun validUUID(value: Any?) = value is String && runCatching { UUID.fromString(value).toString() == value }.getOrDefault(false)
        private fun integer(value: Any?, minimum: Long): Boolean = value is Number && value.toDouble().isFinite() &&
            value.toDouble() == value.toLong().toDouble() && value.toLong() >= minimum && value.toDouble() <= 9007199254740991.0
        @Suppress("UNCHECKED_CAST")
        private fun rows(value: Any?): List<Map<String, Any?>> = if (value == null) emptyList() else
            (value as? List<*>)?.map { it as? Map<String, Any?> ?: error("Invalid backup entry") } ?: error("Invalid backup list")

        @Suppress("UNCHECKED_CAST")
        private fun objectValue(backup: Map<String, Any?>, key: String): Map<String, Any?>? = backup[key]?.let {
            it as? Map<String, Any?> ?: error("Invalid backup $key")
        }

        private fun copyProfile(profile: Profile) = Profile(profile.id, profile.userOrder, profile.name, profile.icon, TypedProfile().apply {
            path = profile.typed.path; type = profile.typed.type; remoteURL = profile.typed.remoteURL
            lastUpdated = Date(profile.typed.lastUpdated.time); autoUpdate = profile.typed.autoUpdate; autoUpdateInterval = profile.typed.autoUpdateInterval
        })
        private fun restoreFile(file: File, original: File) {
            val atomic = AtomicFile(file)
            val output = atomic.startWrite()
            try { original.inputStream().use { it.copyTo(output) }; atomic.finishWrite(output) }
            catch (error: Exception) { atomic.failWrite(output); throw error }
        }
    }
}
