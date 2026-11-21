package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.backend.model.Group
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Profile
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileDefaultTemplate
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.sync.withLock
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** Node operations read and write the selected configuration, including when its service is stopped. */
internal class BoxNodes(
    private val beforeApply: suspend () -> Unit = { ProfileContent.requireStableService() },
    private val applySelected: suspend (recover: Boolean) -> Unit,
) {
    private val mutex = ProfileContent.mutex

    /** List identities and addresses, without exposing node credentials to the page. */
    suspend fun state(): Map<String, Any?> = mutex.withLock {
        val profile = ProfileManager.get(Settings.selectedProfile)
            ?: return@withLock mapOf("profileId" to -1L, "profileRevision" to "", "nodes" to emptyList<Any>(), "groups" to emptyList<Any>())
        val content = File(profile.typed.path).readText()
        val root = JSONObject(Libbox.formatConfig(content).value)
        val entries = listOf("outbounds", "endpoints").flatMap { section ->
            val rows = root.optJSONArray(section) ?: JSONArray()
            (0 until rows.length()).map { rows.getJSONObject(it) }
        }
        val nodes = entries.filter { node ->
            val type = node.optString("type")
            node.optString("tag").isNotBlank() && Libbox.isUIProxyNodeType(type)
        }.map { node ->
            val peer = node.optJSONArray("peers")?.optJSONObject(0)
            mapOf("tag" to node.optString("tag"), "type" to node.optString("type"),
                "server" to node.optString("server").ifEmpty { peer?.optString("address").orEmpty() },
                "port" to node.optInt("server_port", peer?.optInt("port") ?: 0))
        }
        // The running core owns its cache lock and provides live groups through
        // the command stream. Only the stopped local session reads its cache.
        val groups = ProfileContent.snapshotMutex.withLock {
            if (CommandTarget.isRemote || BoxService.currentStatus != Status.Stopped) emptyList()
            else {
                val iterator = Libbox.getUIConfigGroups(content)
                buildList { while (iterator.hasNext()) add(Group(iterator.next()).toWire()) }
            }
        }
        mapOf("profileId" to profile.id, "profileRevision" to ProfileContent.revision(content), "nodes" to nodes, "groups" to groups)
    }

    fun parse(text: String): String = Libbox.parseUINodes(text).value

    /** A failed complete configuration must still be reported as a configuration error. */
    fun detect(text: String, links: Boolean = false): String? = runCatching { parse(text) }.getOrNull()?.takeIf {
        val parsed = JSONObject(it)
        (if (links) parsed.optString("format") == "links" else parsed.optString("hint") != "config") &&
            (parsed.optJSONArray("nodes")?.length() ?: 0) > 0
    }

    private suspend fun selected(profileId: Long, expectedRevision: String? = null): Pair<Profile, String> {
        require(profileId == Settings.selectedProfile) { "Selected profile changed; reload it before saving" }
        val profile = ProfileManager.get(profileId) ?: error("Select a profile before adding nodes")
        val content = File(profile.typed.path).readText()
        require(expectedRevision == null || ProfileContent.revision(content) == expectedRevision) {
            "Configuration changed; reload it before saving"
        }
        return profile to content
    }

    private suspend fun writeProfile(profile: Profile, content: String, expectedRevision: String, renamedNode: Pair<String, String>? = null) =
        ProfileContent.replaceLocked(profile, content, expectedRevision = expectedRevision, renamedNode = renamedNode,
            beforeSave = beforeApply, applySelected = applySelected)

    /** Preserve the core's selector choice without editing or starting its configuration. */
    suspend fun select(group: String, tag: String, profileId: Long, profileRevision: String,
        beforeCommit: () -> Unit = {}) = mutex.withLock {
        val (profile, content) = selected(profileId, profileRevision)
        beforeApply()
        ProfileContent.snapshotMutex.withLock {
            beforeCommit()
            beforeApply()
            require(!CommandTarget.isRemote && BoxService.currentStatus == Status.Stopped) {
                "Wait for the local service to stop before selecting a node"
            }
            require(profile.id == Settings.selectedProfile) { "Selected profile changed; reload it before saving" }
            require(ProfileContent.revision(File(profile.typed.path).readText()) == profileRevision) {
                "Configuration changed; reload it before saving"
            }
            Libbox.selectUIConfigGroup(content, group, tag)
        }
    }

    suspend fun add(nodes: String, profileId: Long, profileRevision: String): Map<String, Any?> = mutex.withLock {
        val (profile, content) = selected(profileId, profileRevision)
        val update = Libbox.addUIConfigNodes(content, nodes, ProfileDefaultTemplate.usesAutoGroup(profile, content))
        writeProfile(profile, update.config, profileRevision)
        mapOf("added" to update.added, "skipped" to update.skipped)
    }

    suspend fun get(tag: String, profileId: Long): Map<String, Any?> = mutex.withLock {
        val (profile, content) = selected(profileId)
        val found = JSONObject(Libbox.getUINode(content, tag).value)
        val nodeContent = found.getString("content")
        mapOf("origin" to "profile", "endpoint" to found.optBoolean("endpoint"), "content" to nodeContent,
            "revision" to ProfileContent.revision(nodeContent), "profileId" to profile.id, "profileRevision" to ProfileContent.revision(content))
    }

    suspend fun share(tags: List<String>, profileId: Long, profileRevision: String): String = mutex.withLock {
        val (_, content) = selected(profileId, profileRevision)
        Libbox.shareUIConfigNodes(content, JSONArray(tags).toString()).value
    }

    suspend fun set(tag: String, content: String, expectedRevision: String?, profileId: Long, profileRevision: String) = mutex.withLock {
        val (profile, previous) = selected(profileId, profileRevision)
        if (tag.isNotEmpty()) {
            val current = JSONObject(Libbox.getUINode(previous, tag).value).getString("content")
            require(expectedRevision != null && ProfileContent.revision(current) == expectedRevision) { "Node changed; reload it before saving" }
        }
        val edited = if (tag.isEmpty()) {
            Libbox.addUIConfigNodes(previous, "[$content]", ProfileDefaultTemplate.usesAutoGroup(profile, previous)).config
        } else Libbox.setUIConfigNode(previous, tag, content).value
        val renamed = if (tag.isNotEmpty()) tag to JSONObject(content).getString("tag") else null
        writeProfile(profile, edited, profileRevision, renamed)
    }

    suspend fun remove(tags: List<String>, profileId: Long, profileRevision: String) = mutex.withLock {
        val (profile, previous) = selected(profileId, profileRevision)
        val content = Libbox.removeUIConfigNodes(previous, JSONArray(tags.distinct()).toString(),
            ProfileDefaultTemplate.usesAutoGroup(profile, previous)).value
        writeProfile(profile, content, profileRevision)
    }
}
