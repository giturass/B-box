package io.nekohasekai.sfa.flutter

import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.job
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.URI
import java.util.concurrent.ConcurrentHashMap

internal class BoxProcessing(private val activity: BoxActivity) {
    private val downloads = ConcurrentHashMap<String, Job>()

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        val session = CommandTarget.localSession()
        fun string(key: String) = args[key] as? String ?: error("Missing $key")
        return when (method) {
            "processing.validate" -> withContext(Dispatchers.IO) {
                jsonObject(JSONObject(Libbox.validateUIProcessingConfig(JSONObject(args["config"] as? Map<*, *>
                    ?: error("Missing processing configuration")).toString())))
            }
            "processing.validateScript" -> withContext(Dispatchers.IO) {
                Libbox.validateUIProcessingScript(string("source"))
                true
            }
            "processing.readModule" -> withContext(Dispatchers.IO) {
                jsonObject(JSONObject(Libbox.readUIProcessingModule(string("text"),
                    JSONObject(args["arguments"] as? Map<*, *> ?: emptyMap<String, String>()).toString(),
                    JSONObject(args["attachments"] as? Map<*, *> ?: emptyMap<String, String>()).toString())))
            }
            "processing.importModule" -> {
                val uri = activity.pickDocument() ?: return null
                withContext(Dispatchers.IO) {
                    CommandTarget.requireLocalSession(session)
                    val (name, bytes) = activity.readDocument(uri)
                    val text = bytes.toString(Charsets.UTF_8)
                    require(!text.contains('\u0000')) { "Module must be text" }
                    mapOf("name" to name, "text" to text)
                }
            }
            "processing.fetchModule" -> {
                val url = string("url")
                val parsed = URI(url)
                require(parsed.scheme in setOf("http", "https") && !parsed.host.isNullOrBlank() && parsed.userInfo == null) {
                    "Module URL must be HTTP(S) with a host and no user credentials"
                }
                val id = string("requestId")
                val owner = currentCoroutineContext().job
                require(downloads.putIfAbsent(id, owner) == null) { "Module download is already running" }
                try {
                    val text = HTTPClient.getString(url)
                    currentCoroutineContext().ensureActive()
                    CommandTarget.requireLocalSession(session)
                    mapOf("url" to url, "text" to text)
                } finally {
                    downloads.remove(id, owner)
                }
            }
            "processing.cancel" -> {
                downloads.remove(string("requestId"))?.cancel()
                true
            }
            "processing.run" -> {
                val id = (args["profileId"] as? Number)?.toLong() ?: error("Missing profile ID")
                val revision = string("revision")
                val name = string("name")
                val running = ProfileContent.mutex.withLock {
                    CommandTarget.requireLocalSession(session)
                    val running = BoxService.dashboardConfig.value
                    require(activity.serviceStatus.value == Status.Started && running != null &&
                        running.profileId == id && Settings.selectedProfile == id &&
                        BoxProfiles.revision(running.content) == revision) {
                        "Start the local service with this saved profile before running its scripts"
                    }
                    running
                }
                withContext(Dispatchers.IO) {
                    CommandTarget.requireLocalSession(session)
                    jsonObject(JSONObject(running.server.runUIProcessingScript(running.content, name)))
                }
            }
            else -> error("Unknown processing operation")
        }.also { CommandTarget.requireLocalSession(session) }
    }
}
