package io.nekohasekai.sfa.flutter

import android.util.AtomicFile
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File

/** Local capture data is deliberately separate from profiles, backups and remote sessions. */
internal class BoxCapture(private val activity: BoxActivity) {
    private val directory = File(activity.noBackupFilesDir, "http-debug")
    private val captures = File(directory, "captures")
    private val settings = AtomicFile(File(directory, "settings.json"))
    private val certificates = CaptureCertificate(activity)

    private companion object {
        // MainActivity and ShareActivity can coexist, but capture and its CA are process-wide.
        val mutex = Mutex()
    }

    private fun requireLocal() {
        require(!CommandTarget.isRemote) { "HTTP debugging is only available for the local service" }
    }

    private fun status() = JSONObject(Libbox.readUICaptureStatus())
    private fun config(): JSONObject = if (settings.baseFile.exists()) JSONObject(String(settings.readFully(), Charsets.UTF_8)) else JSONObject()

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        requireLocal()
        if (method == "capture.export" || method == "capture.ca.export") return export(method, args)
        return mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            Libbox.openUICapture(captures.absolutePath)
            when (method) {
                "capture.ca.snapshot" -> mapOf("state" to jsonObject(status()), "ca" to certificates.snapshot())
                "capture.snapshot" -> mapOf(
                    "state" to jsonObject(status()), "config" to jsonObject(config()), "ca" to certificates.snapshot(),
                    "page" to jsonObject(JSONObject(Libbox.listUICapture(
                        (args["limit"] as? Number)?.toInt()?.coerceIn(1, 300) ?: 50,
                        (args["query"] as? String).orEmpty().take(1024),
                    ))),
                )
                "capture.configure" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its settings" }
                    @Suppress("UNCHECKED_CAST")
                    val value = args["config"] as? Map<String, Any?> ?: error("Missing capture settings")
                    val validated = Libbox.validateUICaptureConfig(JSONObject(value).toString())
                    writeCaptureFile(settings, validated)
                    jsonObject(JSONObject(validated))
                }
                "capture.start" -> {
                    require(activity.serviceStatus.value == Status.Started) { "Start the local service before capturing requests" }
                    val server = BoxService.dashboardServer ?: error("Local service is not running")
                    val value = Libbox.validateUICaptureConfig(config().toString())
                    val ca = if (JSONObject(value).optBoolean("https")) certificates.material() else "" to ""
                    requireLocal()
                    server.startUICapture(captures.absolutePath, value, ca.first, ca.second)
                    if (CommandTarget.isRemote) {
                        Libbox.stopUICapture()
                        requireLocal()
                    }
                    jsonObject(status())
                }
                "capture.stop" -> { Libbox.stopUICapture(); jsonObject(status()) }
                "capture.clear" -> { Libbox.clearUICapture(); null }
                "capture.detail" -> jsonObject(JSONObject(Libbox.readUICapture(args["id"] as? String ?: error("Missing request ID"))))
                "capture.ca.generate" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its certificate" }
                    certificates.material()
                    certificates.snapshot()
                }
                "capture.ca.install" -> certificates.install()
                "capture.ca.status" -> certificates.check()
                "capture.ca.remove" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its certificate" }
                    certificates.remove()
                }
                "capture.ca.rotate" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its certificate" }
                    certificates.rotate()
                }
                else -> error("Unknown capture operation")
            }
        } }
    }

    private suspend fun export(method: String, args: Map<String, Any?>): Boolean {
        val name = if (method == "capture.ca.export") "B-box-HTTP-Debug-CA.crt" else when (args["format"]) {
            "curl" -> "B-box-request.sh"
            "har" -> "B-box-requests.har"
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
                    method == "capture.ca.export" -> file.writeText(certificates.publicCertificate())
                    args["format"] == "curl" -> file.writeText(Libbox.exportUICaptureCurl(args["id"] as? String ?: error("Missing request ID")))
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

internal fun writeCaptureFile(file: AtomicFile, content: String) {
    val parent = file.baseFile.parentFile ?: error("Missing capture directory")
    check(parent.isDirectory || parent.mkdirs()) { "Unable to create capture directory" }
    val stream = file.startWrite()
    try {
        stream.write(content.toByteArray(Charsets.UTF_8))
        file.finishWrite(stream)
    } catch (error: Exception) {
        file.failWrite(stream)
        throw error
    }
}
