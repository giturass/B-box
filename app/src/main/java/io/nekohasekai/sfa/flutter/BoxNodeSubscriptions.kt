package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.database.Profile
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.ProfileNodeSubscription
import io.nekohasekai.sfa.database.ProfileNodeSubscriptionWarning
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.sync.withLock
import org.json.JSONArray
import java.io.File
import java.util.UUID

/** Sources and their nodes are committed together in the configuration selected for import. */
internal class BoxNodeSubscriptions(
    private val beforeApply: suspend () -> Unit = { ProfileContent.requireStableService() },
    private val download: suspend (String) -> String = HTTPClient::getString,
    private val applySelected: suspend (recover: Boolean) -> Unit,
) {
    internal data class Snapshot(
        val profileId: Long, val path: String, val revision: String,
        val sources: List<ProfileNodeSubscription>, val source: ProfileNodeSubscription,
        val importing: Boolean,
    )
    internal data class PreparedImport(val snapshot: Snapshot)

    private suspend fun profile(id: Long): Profile = ProfileManager.get(id) ?: error("Profile no longer exists")

    suspend fun list(profileId: Long): List<Map<String, Any>> = ProfileContent.mutex.withLock {
        profile(profileId).typed.subscriptions.map { it.toWire() }
    }

    private fun metadata(args: Map<String, Any?>, previous: ProfileNodeSubscription? = null): ProfileNodeSubscription {
        val name = (args["name"] as? String)?.trim().orEmpty()
        val url = BoxProfiles.validateUrl((args["url"] as? String)?.trim().orEmpty())
        val interval = args["interval"] as? Number ?: 60
        require(interval.toDouble() == interval.toInt().toDouble() && interval.toInt() >= 15) { "Invalid subscription interval" }
        val source = ProfileNodeSubscription(previous?.id ?: UUID.randomUUID().toString(), name, url,
            args["autoUpdate"] == true, interval.toInt(),
            previous?.updatedAt ?: 0L, previous?.tags.orEmpty(), previous?.skipped ?: 0, previous?.warnings.orEmpty())
        return ProfileNodeSubscription.read(listOf(source.toWire())).single()
    }

    suspend fun prepareImport(args: Map<String, Any?>): PreparedImport = ProfileContent.mutex.withLock {
        val id = (args["profileId"] as? Number)?.toLong() ?: error("Missing target profile")
        require(id == Settings.selectedProfile) { "Selected profile changed; reload it before saving" }
        val profile = profile(id)
        val revision = ProfileContent.revision(File(profile.typed.path).readText())
        require(args["profileRevision"] == revision) { "Configuration changed; reload it before saving" }
        val url = (args["url"] as? String)?.trim().orEmpty()
        val existing = profile.typed.subscriptions.firstOrNull { it.url == url }
        PreparedImport(Snapshot(id, profile.typed.path, revision, profile.typed.subscriptions,
            metadata(args, existing), importing = true))
    }

    suspend fun downloadImport(prepared: PreparedImport): String = download(prepared.snapshot.source.url)

    suspend fun commitImport(prepared: PreparedImport, content: String, beforeCommit: () -> Unit = {}): Map<String, Any> =
        commit(prepared.snapshot, content, beforeCommit)

    suspend fun importSubscription(args: Map<String, Any?>): Map<String, Any> {
        val prepared = prepareImport(args)
        return commitImport(prepared, downloadImport(prepared))
    }

    suspend fun prepareUpdate(profileId: Long, id: String): Snapshot = ProfileContent.mutex.withLock {
        val profile = profile(profileId)
        val source = profile.typed.subscriptions.firstOrNull { it.id == id } ?: error("Subscription no longer exists")
        Snapshot(profileId, profile.typed.path, ProfileContent.revision(File(profile.typed.path).readText()),
            profile.typed.subscriptions, source, importing = false)
    }

    suspend fun downloadUpdate(snapshot: Snapshot): String = download(snapshot.source.url)

    suspend fun commitUpdate(snapshot: Snapshot, content: String, beforeCommit: () -> Unit = {}): Map<String, Any> =
        commit(snapshot, content, beforeCommit)

    suspend fun update(profileId: Long, id: String): Map<String, Any> {
        val snapshot = prepareUpdate(profileId, id)
        return commitUpdate(snapshot, downloadUpdate(snapshot))
    }

    private suspend fun currentLocked(snapshot: Snapshot): Profile {
        if (snapshot.importing) require(snapshot.profileId == Settings.selectedProfile) { "Selected profile changed; reload it before saving" }
        val profile = profile(snapshot.profileId)
        require(profile.typed.path == snapshot.path && profile.typed.subscriptions == snapshot.sources &&
            ProfileContent.revision(File(snapshot.path).readText()) == snapshot.revision) {
            "Profile changed during download; refresh and retry"
        }
        return profile
    }

    private suspend fun commit(snapshot: Snapshot, incoming: String, beforeCommit: () -> Unit): Map<String, Any> = ProfileContent.mutex.withLock {
        beforeCommit()
        currentCoroutineContext().ensureActive()
        val profile = currentLocked(snapshot)
        val content = File(snapshot.path).readText()
        val update = Libbox.applyUISubscriptionNodes(content, incoming, JSONArray(snapshot.source.tags).toString(), profile.typed.isDefault)
        val tags = JSONArray(update.tags)
        val source = snapshot.source.copy(updatedAt = System.currentTimeMillis(), tags = (0 until tags.length()).map { tags.getString(it) },
            skipped = update.skipped, warnings = ProfileNodeSubscriptionWarning.decode(update.warnings))
        val sources = if (snapshot.sources.any { it.id == source.id }) snapshot.sources.map { if (it.id == source.id) source else it }
            else snapshot.sources + source
        ProfileNodeSubscription.read(sources.map { it.toWire() })
        ProfileContent.replaceLocked(profile, update.config, expectedRevision = snapshot.revision, subscriptions = sources,
            beforeCommit = beforeCommit, beforeSave = beforeApply, applySelected = applySelected)
        io.nekohasekai.sfa.bg.UpdateProfileWork.reconfigureUpdater()
        mapOf("added" to update.added, "skipped" to source.skipped, "warnings" to source.warnings.map { it.toWire() }, "source" to source.toWire())
    }

    suspend fun save(args: Map<String, Any?>, beforeCommit: () -> Unit = {}) = ProfileContent.mutex.withLock {
        beforeCommit()
        val profileId = (args["profileId"] as? Number)?.toLong() ?: error("Missing target profile")
        val id = args["id"] as? String ?: error("Missing subscription ID")
        val profile = profile(profileId)
        val sources = profile.typed.subscriptions
        val source = sources.firstOrNull { it.id == id } ?: error("Subscription no longer exists")
        val edited = metadata(args, source)
        require(sources.none { it.id != id && it.url == edited.url }) { "This subscription URL already exists in the profile" }
        val content = File(profile.typed.path).readText()
        ProfileContent.replaceLocked(profile, content, subscriptions = sources.map { if (it.id == id) edited else it },
            beforeCommit = beforeCommit, beforeSave = beforeApply, applySelected = applySelected)
        io.nekohasekai.sfa.bg.UpdateProfileWork.reconfigureUpdater()
    }

    /** Removing a source retains its current nodes as local configuration content. */
    suspend fun delete(profileId: Long, id: String, beforeCommit: () -> Unit = {}) = ProfileContent.mutex.withLock {
        beforeCommit()
        val profile = profile(profileId)
        val sources = profile.typed.subscriptions
        val source = sources.firstOrNull { it.id == id } ?: error("Subscription no longer exists")
        val content = Libbox.retainUISubscriptionNodes(File(profile.typed.path).readText(), JSONArray(source.tags).toString()).value
        ProfileContent.replaceLocked(profile, content, subscriptions = sources.filterNot { it.id == id },
            beforeCommit = beforeCommit, beforeSave = beforeApply, applySelected = applySelected)
        io.nekohasekai.sfa.bg.UpdateProfileWork.reconfigureUpdater()
    }
}
