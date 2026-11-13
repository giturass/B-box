package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.bg.UpdateProfileWork
import io.nekohasekai.sfa.constant.Status
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
    private val applySelected: suspend (restart: Boolean, recover: Boolean) -> Unit,
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
        val profiles = ProfileManager.list().map { profile ->
            val file = File(profile.typed.path)
            val content = file.readText()
            BoxProfiles.validate(content)
            profile.toWire() + mapOf("key" to profileKey(profile, origin), "content" to content)
        }
        val backup = mapOf("format" to "bbox-1", "origin" to origin, "createdAt" to System.currentTimeMillis(),
            "selectedProfile" to Settings.selectedProfile, "profiles" to profiles,
            "preferences" to readPreferences(), "settings" to editableSettings(),
            "terminal" to terminal.backupSnapshot(), "webdav" to webdav.snapshot(),
            "servers" to RemoteServerManager.list().map { mapOf("name" to it.name, "url" to it.url, "secret" to it.secret) })
        backup
    }

    fun encode(backup: Map<String, Any?>): ByteArray = JSONObject(backup).toString().toByteArray()

    suspend fun read(bytes: ByteArray, scope: String): Map<String, Any?> {
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

    suspend fun restore(backup: Map<String, Any?>, scope: String, strategy: String): Int {
        require(strategy in setOf("Compatible", "Override")) { "Invalid restore strategy" }
        val entries = validate(backup, scope)
        val count = ProfileContent.mutex.withLock {
            ProfileContent.requireStableService(activity.serviceStatus.value)
            Restore(backup, entries, full = scope == "all", overriding = strategy == "Override", previous = priorState()).run()
        }
        UpdateProfileWork.reconfigureUpdater()
        return count
    }

    private suspend fun priorState() = PriorState(
        running = activity.serviceStatus.value == Status.Started,
        profiles = ProfileManager.list(),
        servers = RemoteServerManager.list(),
        selection = Settings.selectedProfile,
        mode = Settings.serviceMode,
        preferences = preferences.getString("value", "{}")!!,
        settings = editableSettings(),
        terminal = terminal.snapshot(),
        webdav = webdav.snapshot(),
    )

    /** The state that a failed restore returns to. */
    private class PriorState(
        val running: Boolean,
        val profiles: List<Profile>,
        val servers: List<RemoteServer>,
        val selection: Long,
        val mode: String,
        val preferences: String,
        val settings: Map<String, Any?>,
        val terminal: Map<String, Any?>,
        val webdav: Map<String, Any?>?,
    )

    /** One restore of a validated backup. The caller holds [ProfileContent.mutex] from construction until [run] returns. */
    private inner class Restore(
        private val backup: Map<String, Any?>,
        private val entries: List<Map<String, Any?>>,
        private val full: Boolean,
        private val overriding: Boolean,
        private val previous: PriorState,
    ) {
        private val staging = File(activity.cacheDir, "restore-${UUID.randomUUID()}").also {
            check(it.mkdirs()) { "Unable to stage restore" }
        }

        // Every profile file written so far, with its staged copy or null when the restore created the file.
        private val originals = mutableMapOf<File, File?>()

        // Profile IDs in the backup, to the IDs of the profiles they were restored as.
        private val mapping = mutableMapOf<Long, Long>()
        private val restored = mutableListOf<Profile>()
        private var newFont: File? = null

        // Set when a rollback step fails; the staged copies are then kept.
        private var rollbackFailed = false

        suspend fun run(): Int = try {
            val current = ProfileContent.snapshotMutex.withLock { write() }
            applySelected(full, false)
            publish()
            emit("preferences", readPreferences())
            if (overriding) deleteOrphanedFiles(current)
            restored.size
        } catch (error: Exception) {
            withContext(NonCancellable) { rollBack(error) }
            throw error
        } finally {
            if (!rollbackFailed) staging.deleteRecursively()
        }

        /** Writes the backup and returns the resulting profiles. The caller holds [ProfileContent.snapshotMutex]. */
        private suspend fun write(): List<Profile> {
            ProfileContent.requireStableService(activity.serviceStatus.value)
            ProfileManager.transaction {
                restoreProfiles()
                if (full) restoreServers()
            }
            val current = ProfileManager.list()
            Settings.saveProfileSelection(selection(current))
            if (full) restoreSettings()
            return current
        }

        private suspend fun restoreProfiles() {
            val byKey = previous.profiles.associateBy { profileKey(it, origin()) }
            var nextOrder = (previous.profiles.maxOfOrNull { it.userOrder } ?: -1L) + 1
            for ((index, entry) in entries.withIndex()) {
                val key = entry["key"] as? String
                val id = (entry["id"] as? Number)?.toLong()
                // Profile IDs are only meaningful on the installation that created the backup.
                val existing = key?.let(byKey::get)
                    ?: if (backup["origin"] == origin()) previous.profiles.firstOrNull { it.id == id } else null
                val profile = existing?.let(::copyProfile) ?: Profile()
                val file = existing?.let { File(it.typed.path) }
                    ?: File(File(activity.filesDir, "configs").apply { mkdirs() }, "${key ?: UUID.randomUUID()}.json")
                keepOriginal(file)
                ProfileContent.write(file, entry["content"] as String)
                val url = entry["url"] as? String ?: ""
                profile.name = (entry["name"] as String).trim()
                profile.typed.path = file.path
                profile.typed.remoteURL = url
                profile.typed.type = if (url.isEmpty()) TypedProfile.Type.Local else TypedProfile.Type.Remote
                profile.typed.autoUpdate = url.isNotEmpty() && entry["autoUpdate"] == true
                profile.typed.autoUpdateInterval = (entry["interval"] as? Number)?.toInt() ?: 60
                profile.typed.lastUpdated = Date((entry["updatedAt"] as? Number)?.toLong() ?: 0L)
                profile.userOrder = if (overriding) index.toLong() else existing?.userOrder ?: nextOrder++
                if (existing == null) ProfileManager.create(profile) else ProfileManager.update(profile)
                restored.add(profile)
                if (id != null) mapping[id] = profile.id
            }
            if (overriding) ProfileManager.delete(previous.profiles.filter { old -> restored.none { it.id == old.id } })
        }

        private fun keepOriginal(file: File) {
            if (file in originals) return
            originals[file] = if (file.exists()) file.copyTo(File(staging, originals.size.toString())) else null
        }

        private suspend fun restoreServers() {
            val kept = mutableSetOf<Long>()
            for ((index, entry) in rows(backup["servers"]).withIndex()) {
                val name = entry["name"] as String
                val url = RemoteServer.validateURL(entry["url"] as String)!!
                val existing = previous.servers.firstOrNull { RemoteServer.normalizeURL(it.url) == url && it.name == name }
                val server = RemoteServer(existing?.id ?: 0L, existing?.userOrder ?: 0L, name, url, entry["secret"] as? String ?: "")
                if (existing == null) RemoteServerManager.create(server) else RemoteServerManager.update(server)
                if (overriding) {
                    server.userOrder = index.toLong()
                    RemoteServerManager.update(server)
                }
                kept.add(server.id)
            }
            if (overriding) previous.servers.filter { it.id !in kept }.forEach { RemoteServerManager.delete(it) }
        }

        // A full restore follows the selection in the backup. Otherwise the previous selection stays while its profile exists.
        private fun selection(current: List<Profile>): Long {
            val selected = if (full) (backup["selectedProfile"] as? Number)?.toLong() else null
            if (selected == -1L) return -1L
            return mapping[selected]
                ?: previous.selection.takeIf { id -> current.any { it.id == id } }
                ?: restored.firstOrNull()?.id
                ?: -1L
        }

        private suspend fun restoreSettings() {
            writeSettings(objectValue(backup, "settings") ?: emptyMap())
            objectValue(backup, "terminal")?.let { newFont = terminal.restoreBackup(it) }
            if (backup.containsKey("webdav")) webdav.save(objectValue(backup, "webdav"))
            val restoredPreferences = BoxPreferences.validate(objectValue(backup, "preferences") ?: emptyMap()).toMutableMap()
            val folds = objectValue(restoredPreferences, "proxyUnfolded")
            if (folds != null) restoredPreferences["proxyUnfolded"] = folds.mapKeys { (key, _) -> restoredProfileKey(key) }
            val saved = preferences.edit().putString("value", JSONObject(restoredPreferences).toString()).commit()
            check(saved) { "Unable to restore preferences" }
        }

        // Group folding is saved under "local:<profile ID>", and a restored profile may have received a new ID.
        private fun restoredProfileKey(key: String): String {
            if (!key.startsWith("local:")) return key
            val id = mapping[key.removePrefix("local:").toLongOrNull()] ?: return key
            return "local:$id"
        }

        private fun deleteOrphanedFiles(current: List<Profile>) {
            val kept = current.map { it.typed.path }.toSet()
            previous.profiles.filter { it.typed.path !in kept }.forEach { runCatching { File(it.typed.path).delete() } }
        }

        /** Puts the previous state back. A failed step is attached to [error] and does not stop the remaining steps. */
        private suspend fun rollBack(error: Exception) {
            suspend fun attempt(step: suspend () -> Unit) {
                try {
                    step()
                } catch (failure: Exception) {
                    rollbackFailed = true
                    error.addSuppressed(failure)
                }
            }
            ProfileContent.snapshotMutex.withLock {
                attempt {
                    ProfileManager.transaction {
                        rollBackProfiles()
                        if (full) rollBackServers()
                    }
                }
                for ((file, original) in originals) attempt { rollBackFile(file, original) }
                attempt { Settings.saveProfileSelection(previous.selection, previous.mode) }
                if (full) {
                    attempt { writeSettings(previous.settings) }
                    attempt { terminal.restoreState(previous.terminal) }
                    attempt { webdav.save(previous.webdav) }
                    attempt { check(preferences.edit().putString("value", previous.preferences).commit()) }
                }
                attempt { newFont?.let { check(!it.exists() || it.delete()) { "Unable to remove restored font" } } }
            }
            // Never start a mixture of restored and unrestored data.
            if (previous.running && !rollbackFailed) attempt { applySelected(true, true) }
            attempt {
                publish()
                emit("preferences", readPreferences())
            }
        }

        private suspend fun rollBackProfiles() {
            ProfileManager.delete(ProfileManager.list().filter { now -> previous.profiles.none { it.id == now.id } })
            for (profile in previous.profiles) {
                if (ProfileManager.get(profile.id) == null) ProfileManager.create(profile) else ProfileManager.update(profile)
            }
        }

        private suspend fun rollBackServers() {
            val added = RemoteServerManager.list().filter { now -> previous.servers.none { it.id == now.id } }
            added.forEach { RemoteServerManager.delete(it) }
            for (server in previous.servers) {
                if (RemoteServerManager.get(server.id) != null) {
                    RemoteServerManager.update(server)
                    continue
                }
                // Creating a server assigns the next free order, so the previous order is written afterwards.
                val copy = RemoteServer(server.id, server.userOrder, server.name, server.url, server.secret)
                RemoteServerManager.create(copy)
                copy.userOrder = server.userOrder
                RemoteServerManager.update(copy)
            }
        }

        private fun rollBackFile(file: File, original: File?) {
            if (original != null) {
                restoreFile(file, original)
            } else if (file.exists()) {
                check(file.delete())
            }
        }
    }

    companion object {
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

        private fun copyProfile(profile: Profile): Profile {
            val typed = TypedProfile().apply {
                path = profile.typed.path
                type = profile.typed.type
                remoteURL = profile.typed.remoteURL
                lastUpdated = Date(profile.typed.lastUpdated.time)
                autoUpdate = profile.typed.autoUpdate
                autoUpdateInterval = profile.typed.autoUpdateInterval
            }
            return Profile(profile.id, profile.userOrder, profile.name, profile.icon, typed)
        }

        private fun restoreFile(file: File, original: File) {
            ProfileContent.write(file) { output -> original.inputStream().use { it.copyTo(output) } }
        }
    }
}
