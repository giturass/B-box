package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.database.NodeLibrary
import io.nekohasekai.sfa.database.Profile
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.sync.withLock
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.URI
import java.util.UUID

/** The shared nodes behind the `nodes.*` bridge methods. Node content stays the text libbox wrote. */
internal class BoxNodes(
    private val beforeApply: suspend () -> Unit = { ProfileContent.requireStableService() },
    private val download: suspend (String) -> String = HTTPClient::getString,
    private val applySelected: suspend (recover: Boolean) -> Unit,
) {
    private val mutex = ProfileContent.mutex

    /** What the interface lists: names and addresses without the credentials of a node. */
    fun state(): Map<String, Any?> {
        val text = NodeLibrary.read()
        val root = if (text.isBlank()) JSONObject() else JSONObject(text)
        val entries = root.optJSONArray("nodes") ?: JSONArray()
        val counts = mutableMapOf<String, Int>()
        val nodes = (0 until entries.length()).map { index ->
            val entry = entries.getJSONObject(index)
            val node = entry.getJSONObject("node")
            val source = entry.optString("source")
            counts[source] = (counts[source] ?: 0) + 1
            val peer = node.optJSONArray("peers")?.optJSONObject(0)
            mapOf("tag" to node.optString("tag"), "type" to node.optString("type"), "source" to source,
                "server" to node.optString("server").ifEmpty { peer?.optString("address").orEmpty() },
                "port" to node.optInt("server_port", peer?.optInt("port") ?: 0))
        }
        val sources = root.optJSONArray("sources") ?: JSONArray()
        return mapOf("nodes" to nodes, "sources" to (0 until sources.length()).map { index ->
            sources.getJSONObject(index).toWire(counts)
        })
    }

    private fun JSONObject.toWire(counts: Map<String, Int>): Map<String, Any?> = mapOf(
        "id" to getString("id"), "name" to optString("name"), "url" to optString("url"),
        "updatedAt" to optLong("updated"), "autoUpdate" to optBoolean("auto_update"),
        "interval" to optInt("interval", 60), "count" to (counts[getString("id")] ?: 0))

    fun parse(text: String): String = Libbox.parseUINodes(text).value

    /** The nodes of a text that was not written as a configuration, or null when it holds none. */
    fun detect(text: String, links: Boolean = false): String? = runCatching { parse(text) }.getOrNull()?.takeIf {
        val parsed = JSONObject(it)
        // A whole configuration that failed its check is not offered as a list of nodes.
        (if (links) parsed.optString("format") == "links" else parsed.optString("hint") != "config") &&
            (parsed.optJSONArray("nodes")?.length() ?: 0) > 0
    }

    private suspend fun selected(): Pair<Profile, String>? {
        val profile = ProfileManager.get(Settings.selectedProfile) ?: return null
        val file = File(profile.typed.path)
        return if (file.isFile) profile to file.readText() else null
    }

    private suspend fun replace(library: String) = NodeLibrary.replaceLocked(library, beforeApply, applySelected)

    private suspend fun writeProfile(profile: Profile, content: String) =
        ProfileContent.replaceLocked(profile, content, beforeSave = beforeApply, applySelected = applySelected)

    // A profile that names a shared node would no longer start without it.
    private suspend fun requireUnused(tags: List<String>) {
        if (tags.isEmpty()) return
        val names = JSONArray(tags).toString()
        for (profile in ProfileManager.list()) {
            val file = File(profile.typed.path)
            if (!file.isFile) continue
            val found = JSONArray(Libbox.findUINodeReferences(file.readText(), names).value)
            if (found.length() == 0) continue
            val first = found.getJSONObject(0)
            error("${first.getString("tag")} is used by ${profile.name} at ${first.getString("path")}")
        }
    }

    /** Adds nodes entered by hand, as the JSON array or parse result libbox returned. */
    suspend fun add(nodes: String): Map<String, Any?> = mutex.withLock {
        val update = Libbox.addUINodes(NodeLibrary.read(), "", nodes)
        replace(update.library)
        mapOf("added" to update.added, "skipped" to update.skipped)
    }

    /** The node as the selected profile sees it, with a revision for the save that follows. */
    suspend fun get(tag: String): Map<String, Any?> = mutex.withLock { find(selected()?.second.orEmpty(), NodeLibrary.read(), tag) }

    private fun find(config: String, library: String, tag: String): Map<String, Any?> {
        val found = JSONObject(Libbox.getUINode(config, library, tag).value)
        val content = found.getString("content")
        return mapOf("origin" to found.getString("origin"), "source" to found.optString("source"),
            "endpoint" to found.optBoolean("endpoint"), "content" to content, "revision" to ProfileContent.revision(content))
    }

    /** Saves an edited node where it is defined. An empty tag adds a shared node. */
    suspend fun set(tag: String, content: String, expectedRevision: String?) = mutex.withLock {
        val library = NodeLibrary.read()
        if (tag.isEmpty()) {
            replace(Libbox.setUINode(library, "", content).value)
            return@withLock
        }
        val selected = selected()
        val current = find(selected?.second.orEmpty(), library, tag)
        require(expectedRevision == null || current["revision"] == expectedRevision) { "Node changed; reload it before saving" }
        if (current["origin"] == "profile") {
            writeProfile(selected!!.first, Libbox.setUIConfigNode(selected.second, tag, content).value)
        } else {
            val edited = Libbox.setUINode(library, tag, content).value
            val name = runCatching { JSONObject(content).optString("tag") }.getOrDefault(tag)
            if (name != tag) requireUnused(listOf(tag))
            replace(edited)
        }
    }

    /** Removes nodes from the selected profile or the shared nodes, whichever defines each. */
    suspend fun remove(tags: List<String>) = mutex.withLock {
        val library = NodeLibrary.read()
        val selected = selected()
        val (own, shared) = tags.distinct().partition { find(selected?.second.orEmpty(), library, it)["origin"] == "profile" }
        // Both documents are prepared before either is written.
        val config = if (own.isEmpty()) null else Libbox.removeUIConfigNodes(selected!!.second, JSONArray(own).toString()).value
        val remaining = if (shared.isEmpty()) null else Libbox.removeUINodes(library, JSONArray(shared).toString()).value
        requireUnused(shared)
        if (config != null) writeProfile(selected!!.first, config)
        if (remaining != null) replace(remaining)
    }

    internal class Subscription(val id: String, val name: String, val url: String, val autoUpdate: Boolean, val interval: Int)

    fun subscription(args: Map<String, Any?>): Subscription {
        val url = (args["url"] as? String)?.trim().orEmpty()
        val address = runCatching { URI(url) }.getOrNull()
        require(address != null && address.scheme in listOf("https", "http") && !address.host.isNullOrBlank()) { "Enter an HTTP or HTTPS subscription URL" }
        val interval = (args["interval"] as? Number)?.toInt() ?: 60
        require(interval >= 15) { "Update interval must be at least 15 minutes" }
        val name = (args["name"] as? String)?.trim().orEmpty().ifEmpty { address.host }
        return Subscription((args["id"] as? String).orEmpty(), name, url, args["autoUpdate"] != false, interval)
    }

    suspend fun subscription(id: String): Subscription = mutex.withLock {
        val source = source(NodeLibrary.read(), id) ?: error("Subscription no longer exists")
        Subscription(id, source.optString("name"), source.optString("url"), source.optBoolean("auto_update"), source.optInt("interval", 60))
    }

    private fun source(library: String, id: String): JSONObject? {
        if (library.isBlank()) return null
        val sources = JSONObject(library).optJSONArray("sources") ?: return null
        return (0 until sources.length()).map { sources.getJSONObject(it) }.firstOrNull { it.optString("id") == id }
    }

    /** Downloads outside every lock. The result is the text libbox parsed the subscription into. */
    suspend fun fetch(subscription: Subscription): String = parse(download(subscription.url))

    /** Stores a subscription with the nodes it served, replacing those it served before. */
    suspend fun commit(subscription: Subscription, parsed: String, beforeCommit: () -> Unit = {}): Map<String, Any?> = mutex.withLock {
        beforeCommit()
        val current = NodeLibrary.read()
        val id = subscription.id.ifEmpty { UUID.randomUUID().toString() }
        require(subscription.id.isEmpty() || source(current, id) != null) { "Subscription no longer exists" }
        val source = JSONObject().put("id", id).put("name", subscription.name).put("url", subscription.url)
            .put("auto_update", subscription.autoUpdate).put("interval", subscription.interval).put("updated", System.currentTimeMillis())
        val update = Libbox.replaceUINodeSource(Libbox.setUINodeSource(current, source.toString()).value, id, parsed)
        replace(update.library)
        mapOf("added" to update.added, "skipped" to JSONObject(parsed).optJSONArray("skipped")?.length())
    }

    /** Changes what is known about a subscription without reading it again. */
    suspend fun saveSource(subscription: Subscription) = mutex.withLock {
        val library = NodeLibrary.read()
        val source = source(library, subscription.id) ?: error("Subscription no longer exists")
        source.put("name", subscription.name).put("url", subscription.url)
            .put("auto_update", subscription.autoUpdate).put("interval", subscription.interval)
        replace(Libbox.setUINodeSource(library, source.toString()).value)
    }

    suspend fun removeSource(id: String) = mutex.withLock {
        val library = NodeLibrary.read()
        val removed = Libbox.removeUINodeSource(library, id).value
        val entries = (if (library.isBlank()) null else JSONObject(library).optJSONArray("nodes")) ?: JSONArray()
        requireUnused((0 until entries.length()).map { entries.getJSONObject(it) }
            .filter { it.optString("source") == id }.map { it.getJSONObject("node").optString("tag") })
        replace(removed)
    }
}
