package io.nekohasekai.sfa.flutter

import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.IOException

internal class BoxPanelCache(private val directory: File) {
    private companion object {
        val access = Any()
        const val maxBytes = 1024 * 1024
    }

    private fun file(id: Long) = File(directory, "surge-panels-$id.json")
    private fun key(moduleId: String, name: String) = JSONArray().put(moduleId).put(name).toString()
    private fun entries(id: Long, revision: String): JSONObject = runCatching {
        val source = file(id)
        if (!source.isFile || source.length() > maxBytes) return@runCatching null
        val data = JSONObject(source.readText())
        data.takeIf { it.optString("revision") == revision }?.optJSONObject("entries")
    }.getOrNull() ?: JSONObject()

    fun withCached(id: Long, revision: String, list: String): String = synchronized(access) {
        val cached = entries(id, revision)
        val panels = JSONArray(list)
        for (index in 0 until panels.length()) {
            val panel = panels.getJSONObject(index)
            cached.optJSONObject(key(panel.getString("moduleId"), panel.getString("name")))?.let { panel.put("cached", it) }
        }
        panels.toString()
    }

    fun save(id: Long, revision: String, moduleId: String, name: String, result: String): JSONObject = synchronized(access) {
        val value = runCatching { JSONObject(result) }.getOrNull()
        require(value != null && (value.opt("title") as? String)?.isNotBlank() == true) { "Panel script returned no title" }
        val panel = JSONObject().put("updatedAt", System.currentTimeMillis())
        for (field in listOf("title", "content", "style", "icon", "icon-color")) {
            (value.opt(field) as? String)?.let { panel.put(field, it) }
        }
        val cached = entries(id, revision)
        val current = key(moduleId, name)
        cached.put(current, panel)
        val wrapper = JSONObject().put("revision", revision).put("entries", cached)
        var data = wrapper.toString().toByteArray(Charsets.UTF_8)
        if (data.size > maxBytes) {
            val oldest = cached.keys().asSequence().filterIsInstance<String>().filter { it != current }
                .sortedBy { cached.optJSONObject(it)?.optLong("updatedAt") ?: 0L }.toList()
            var excess = data.size - maxBytes
            for (entry in oldest) {
                excess -= cached.getJSONObject(entry).toString().toByteArray(Charsets.UTF_8).size + entry.toByteArray(Charsets.UTF_8).size + 3
                cached.remove(entry)
                if (excess <= 0) break
            }
            data = wrapper.toString().toByteArray(Charsets.UTF_8)
        }
        if (data.size <= maxBytes) {
            try {
                val pending = File.createTempFile(".surge-panel-", ".tmp", directory)
                try {
                    pending.outputStream().use { it.write(data); it.fd.sync() }
                    pending.renameTo(file(id))
                } finally { pending.delete() }
            } catch (_: IOException) { }
        }
        panel
    }
}
