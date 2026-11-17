package io.nekohasekai.sfa.database

import io.nekohasekai.libbox.Libbox
import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.util.UUID

data class ProfileNodeSubscriptionWarning(val reason: String, val count: Int) {
    fun toWire(): Map<String, Any> = mapOf("reason" to reason, "count" to count)

    companion object {
        fun read(value: Any?): List<ProfileNodeSubscriptionWarning> {
            val list = value as? List<*> ?: error("Invalid subscription warnings")
            val warnings = list.map { item ->
                val entry = item as? Map<*, *> ?: error("Invalid subscription warning")
                val reason = entry["reason"] as? String ?: error("Missing subscription warning reason")
                require(reason.isNotBlank()) { "Invalid subscription warning reason" }
                val count = entry["count"] as? Number ?: error("Invalid subscription warning count")
                require(count.toDouble() == count.toInt().toDouble() && count.toInt() > 0) { "Invalid subscription warning count" }
                ProfileNodeSubscriptionWarning(reason, count.toInt())
            }
            require(warnings.map { it.reason }.distinct().size == warnings.size) { "Duplicate subscription warning reasons" }
            return warnings
        }

        fun decode(content: String): List<ProfileNodeSubscriptionWarning> {
            val warnings = JSONArray(content)
            return read((0 until warnings.length()).map { index ->
                val entry = warnings.getJSONObject(index)
                mapOf("reason" to entry.getString("reason"), "count" to entry.get("count"))
            })
        }
    }
}

/** Subscription ownership belongs to a configuration, never to a shared node database. */
data class ProfileNodeSubscription(
    val id: String,
    val name: String,
    val url: String,
    val autoUpdate: Boolean,
    val interval: Int,
    val updatedAt: Long,
    val tags: List<String>,
    val skipped: Int = 0,
    val warnings: List<ProfileNodeSubscriptionWarning> = emptyList(),
) {
    fun toWire(): Map<String, Any> = mapOf(
        "id" to id, "name" to name, "url" to url, "autoUpdate" to autoUpdate,
        "interval" to interval, "updatedAt" to updatedAt, "tags" to tags,
        "skipped" to skipped, "warnings" to warnings.map { it.toWire() },
    )

    companion object {
        fun read(value: Any?): List<ProfileNodeSubscription> {
            if (value == null) return emptyList()
            val list = value as? List<*> ?: error("Invalid node subscriptions")
            val sources = list.map { item ->
                val entry = item as? Map<*, *> ?: error("Invalid node subscription")
                val id = entry["id"] as? String ?: error("Missing subscription ID")
                require(runCatching { UUID.fromString(id).toString() == id }.getOrDefault(false)) { "Invalid subscription ID" }
                val name = (entry["name"] as? String)?.trim().orEmpty()
                require(name.isNotEmpty()) { "A subscription name is required" }
                val url = (entry["url"] as? String)?.trim().orEmpty()
                val uri = URI(url)
                require(uri.scheme in listOf("http", "https") && !uri.host.isNullOrBlank()) { "Invalid node subscription URL" }
                val auto = entry["autoUpdate"] as? Boolean ?: error("Invalid subscription auto update option")
                val interval = entry["interval"] as? Number ?: error("Invalid subscription interval")
                require(interval.toDouble() == interval.toInt().toDouble() && interval.toInt() >= 15) { "Invalid subscription interval" }
                val updatedAt = entry["updatedAt"] as? Number ?: error("Invalid subscription timestamp")
                require(updatedAt.toDouble() == updatedAt.toLong().toDouble() && updatedAt.toLong() >= 0L) { "Invalid subscription timestamp" }
                val tags = (entry["tags"] as? List<*>)?.map {
                    (it as? String)?.takeIf(String::isNotBlank) ?: error("Invalid subscription node name")
                } ?: error("Invalid subscription node names")
                require(tags.distinct().size == tags.size) { "Duplicate subscription node names" }
                val skipped = if (entry.containsKey("skipped")) entry["skipped"] as? Number ?: error("Invalid skipped subscription count") else 0
                require(skipped.toDouble() == skipped.toInt().toDouble() && skipped.toInt() >= 0) { "Invalid skipped subscription count" }
                val warnings = if (entry.containsKey("warnings")) ProfileNodeSubscriptionWarning.read(entry["warnings"]) else emptyList()
                require(warnings.sumOf { it.count.toLong() } == skipped.toLong()) { "Subscription warning counts do not match skipped nodes" }
                ProfileNodeSubscription(id, name, url, auto, interval.toInt(), updatedAt.toLong(), tags, skipped.toInt(), warnings)
            }
            require(sources.map { it.id }.distinct().size == sources.size) { "Duplicate node subscriptions" }
            require(sources.map { it.url }.distinct().size == sources.size) { "Duplicate node subscription URLs" }
            val tags = sources.flatMap { it.tags }
            require(tags.distinct().size == tags.size) { "Node belongs to multiple subscriptions" }
            return sources
        }

        fun encode(sources: List<ProfileNodeSubscription>): String = JSONArray(sources.map { it.toWire() }).toString()

        fun decode(content: String): List<ProfileNodeSubscription> {
            val list = JSONArray(content)
            return read((0 until list.length()).map { index ->
                val entry = list.getJSONObject(index)
                val tags = entry.getJSONArray("tags")
                val wire = mutableMapOf<String, Any>("id" to entry.getString("id"), "name" to entry.getString("name"), "url" to entry.getString("url"),
                    "autoUpdate" to entry.getBoolean("autoUpdate"), "interval" to entry.get("interval"),
                    "updatedAt" to entry.get("updatedAt"), "tags" to (0 until tags.length()).map { tags.getString(it) })
                if (entry.has("skipped")) wire["skipped"] = entry.get("skipped")
                if (entry.has("warnings")) wire["warnings"] = ProfileNodeSubscriptionWarning.decode(entry.getJSONArray("warnings").toString()).map { it.toWire() }
                wire
            })
        }

        fun proxyTags(content: String): List<String> {
            val root = JSONObject(Libbox.formatConfig(content).value)
            return listOf("outbounds", "endpoints").flatMap { key ->
                val list = root.optJSONArray(key) ?: JSONArray()
                (0 until list.length()).map { list.getJSONObject(it) }
            }.filter {
                it.optString("type") !in setOf("direct", "block", "dns", "bridge", "selector", "urltest", "tailscale") &&
                    !it.optString("type").endsWith("-server") && it.optString("tag").isNotBlank()
            }.map { it.getString("tag") }
        }
    }
}
