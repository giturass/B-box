package io.nekohasekai.sfa.flutter

import android.os.Binder
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.UICaptureSource
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.bg.ICaptureSession
import io.nekohasekai.sfa.bg.RootClient
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import org.json.JSONArray
import java.io.File

/** Local passive HTTPS capture and routed HTTP recording. */
internal class BoxCapture(private val activity: BoxActivity) {
    private val directory = File(activity.noBackupFilesDir, "http-debug")
    private val captures = File(directory, "captures")
    private val settings = CaptureSettings(directory)

    private companion object {
        val mutex = Mutex()
    }

    private class CaptureSource : UICaptureSource {
        val owner = Binder()
        var session: ICaptureSession? = null
        override fun close() { runCatching { session?.close() } }
    }

    private fun requireLocal() {
        require(!CommandTarget.isRemote) { "HTTP debugging is only available for the local service" }
    }
    private fun status() = JSONObject(Libbox.readUICaptureStatus())

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        requireLocal()
        if (method == "capture.export") return export(args)
        return mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            if (method != "capture.logs" && method != "capture.logs.clear") Libbox.openUICapture(captures.absolutePath)
            when (method) {
                "capture.snapshot" -> mapOf(
                    "state" to jsonObject(status()),
                    "page" to jsonObject(JSONObject(Libbox.listUICapture(
                        (args["limit"] as? Number)?.toInt()?.coerceIn(1, 300) ?: 50,
                        (args["query"] as? String).orEmpty().take(1024),
                    ))),
                )
                "capture.settings" -> settings.snapshot()
                "capture.logs" -> jsonObject(JSONObject(Libbox.readUICaptureLogs()))
                "capture.logs.clear" -> { Libbox.clearUICaptureLogs(); null }
                "capture.configure" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its settings" }
                    @Suppress("UNCHECKED_CAST")
                    val value = args["config"] as? Map<String, Any?> ?: error("Missing capture settings")
                    settings.save(args["revision"] as? String ?: error("Missing capture settings revision"), JSONObject(value).toString())
                }
                "capture.start" -> start()
                "capture.stop" -> { Libbox.stopUICapture(); jsonObject(status()) }
                "capture.clear" -> { Libbox.clearUICapture(); null }
                "capture.domains" -> {
                    val domains = JSONArray(Libbox.extractUICaptureHosts((args["query"] as? String).orEmpty().take(1024)))
                    List(domains.length()) { domains.getString(it) }
                }
                "capture.detail" -> jsonObject(JSONObject(Libbox.readUICapture(args["id"] as? String ?: error("Missing request ID"))))
                else -> error("Unknown capture operation")
            }
        } }
    }

    private suspend fun start(): Map<String, Any?> {
        require(!status().optBoolean("active")) { "Capture is already running" }
        require(activity.serviceStatus.value == Status.Started) { "Start the local service before capturing requests" }
        val running = BoxService.dashboardConfig.value ?: error("Local service is not running")
        val value = settings.config()
        val source = if (JSONObject(value).optBoolean("https")) CaptureSource() else null
        try {
            if (source != null) {
                Libbox.appendUICaptureLog("info", "Starting eBPF capture; requesting the Root collector")
                source.session = RootClient.startCapture(value, source.owner)
            }
            return ProfileContent.mutex.withLock {
                requireLocal()
                require(activity.serviceStatus.value == Status.Started &&
                    running.profileId == Settings.selectedProfile &&
                    running === BoxService.dashboardConfig.value && running.server === BoxService.dashboardServer) {
                    "Local service changed; start capture again"
                }
                if (source != null) {
                    source.session!!.fileDescriptor.use { descriptor ->
                        running.server.startUICapture(captures.absolutePath, value, descriptor.fd, source)
                    }
                } else {
                    running.server.startUICapture(captures.absolutePath, value, -1, null)
                }
                if (CommandTarget.isRemote) { Libbox.stopUICapture(); requireLocal() }
                val state = status()
                check(state.optBoolean("active")) { state.optString("error", "Capture startup failed") }
                jsonObject(state)
            }
        } catch (error: Exception) {
            source?.close()
            if (source != null) {
                if (error is CancellationException) {
                    Libbox.appendUICaptureLog("warn", "eBPF capture startup cancelled")
                    throw error
                }
                (error.message ?: error.javaClass.simpleName).lineSequence().forEach { line ->
                    val level = listOf("debug", "info", "warn", "error").firstOrNull { line.startsWith("[$it] ") }
                    Libbox.appendUICaptureLog(level ?: "error", if (level == null) line else line.removePrefix("[$level] "))
                }
                throw IllegalStateException("Unable to start HTTPS capture. See eBPF logs for details.", error)
            }
            throw error
        }
    }

    private suspend fun export(args: Map<String, Any?>): Boolean {
        val name = when (args["format"]) {
            "curl" -> "B-box-request.sh"
            "har" -> "B-box-requests.har"
            "domains" -> "B-box-domains.txt"
            else -> error("Unknown capture export format")
        }
        val destination = activity.pickDocument(name) ?: return false
        requireLocal()
        return mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            Libbox.openUICapture(captures.absolutePath)
            check(directory.isDirectory || directory.mkdirs()) { "Unable to create capture directory" }
            val file = File.createTempFile("export-", ".tmp", directory)
            try {
                when {
                    args["format"] == "curl" -> file.writeText(Libbox.exportUICaptureCurl(args["id"] as? String ?: error("Missing request ID")))
                    args["format"] == "domains" -> {
                        val domains = JSONArray(Libbox.extractUICaptureHosts((args["query"] as? String).orEmpty().take(1024)))
                        file.writeText(List(domains.length()) { domains.getString(it) }.joinToString("\n", postfix = "\n"))
                    }
                    else -> Libbox.exportUICaptureHAR(file.absolutePath, (args["query"] as? String).orEmpty().take(1024))
                }
                activity.contentResolver.openOutputStream(destination, "wt")?.use { output ->
                    file.inputStream().use { it.copyTo(output) }
                } ?: error("Unable to export capture")
                true
            } finally { file.delete() }
        } }
    }
}
