package io.nekohasekai.sfa.flutter

import android.util.AtomicFile
import android.util.Base64
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import org.json.JSONArray
import java.io.File
import java.nio.ByteBuffer

/** Rules belong to the profile; records and private CA material stay local. */
internal class BoxCapture(private val activity: BoxActivity, private val profiles: BoxProfiles) {
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
        if (method == "capture.ca.export" || method == "capture.ca.module") return exportCertificate(method == "capture.ca.module")
        if (method == "capture.export") return export(args)
        if (method == "capture.import") return importResource(args)
        return mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            Libbox.openUICapture(captures.absolutePath)
            when (method) {
                "capture.ca.snapshot" -> mapOf("state" to jsonObject(status()), "ca" to certificates.snapshot())
                "capture.snapshot" -> mapOf(
                    "state" to jsonObject(status()), "ca" to certificates.snapshot(),
                    "page" to jsonObject(JSONObject(Libbox.listUICapture(
                        (args["limit"] as? Number)?.toInt()?.coerceIn(1, 300) ?: 50,
                        (args["query"] as? String).orEmpty().take(1024),
                    ))),
                )
                "capture.settings" -> profiles.captureSettings(config().toString())
                "capture.validate" -> {
                    @Suppress("UNCHECKED_CAST")
                    val value = args["config"] as? Map<String, Any?> ?: error("Missing capture settings")
                    jsonObject(JSONObject(Libbox.validateUICaptureConfig(JSONObject(value).toString())))
                }
                "capture.configure" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its settings" }
                    @Suppress("UNCHECKED_CAST")
                    val value = args["config"] as? Map<String, Any?> ?: error("Missing capture settings")
                    val validated = Libbox.validateUICaptureConfig(JSONObject(value).toString())
                    profiles.setCaptureSettings(
                        (args["profileId"] as? Number)?.toLong() ?: error("Missing capture profile"),
                        args["revision"] as? String ?: error("Missing configuration revision"),
                        validated,
                    )
                    jsonObject(JSONObject(validated))
                }
                "capture.start" -> ProfileContent.mutex.withLock {
                    require(activity.serviceStatus.value == Status.Started) { "Start the local service before capturing requests" }
                    val running = BoxService.dashboardConfig.value ?: error("Local service is not running")
                    require(running.profileId == Settings.selectedProfile && running.server === BoxService.dashboardServer) {
                        "Selected profile changed; reload it before capturing"
                    }
                    val embedded = Libbox.readUICaptureConfig(running.content)
                    val value = Libbox.validateUICaptureConfig(embedded.ifEmpty { config().toString() })
                    val ca = if (JSONObject(value).optBoolean("https")) certificates.material() else "" to ""
                    requireLocal()
                    running.server.startUICapture(captures.absolutePath, value, ca.first, ca.second)
                    if (CommandTarget.isRemote) {
                        Libbox.stopUICapture()
                        requireLocal()
                    }
                    jsonObject(status())
                }
                "capture.stop" -> { Libbox.stopUICapture(); jsonObject(status()) }
                "capture.clear" -> { Libbox.clearUICapture(); null }
                "capture.domains" -> {
                    val domains = JSONArray(Libbox.extractUICaptureHosts((args["query"] as? String).orEmpty().take(1024)))
                    List(domains.length()) { domains.getString(it) }
                }
                "capture.detail" -> jsonObject(JSONObject(Libbox.readUICapture(args["id"] as? String ?: error("Missing request ID"))))
                "capture.ca.generate" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its certificate" }
                    certificates.material()
                    certificates.snapshot()
                }
                "capture.ca.status" -> certificates.check()
                "capture.ca.rotate" -> {
                    require(!status().optBoolean("active")) { "Stop capture before changing its certificate" }
                    certificates.rotate()
                }
                else -> error("Unknown capture operation")
            }
        } }
    }

    private suspend fun importResource(args: Map<String, Any?>): Map<String, Any?>? {
        val script = args["kind"] == "script"
        require(script || args["kind"] == "mapLocal") { "Unknown capture resource type" }
        val document = activity.pickDocument() ?: return null
        return withContext(Dispatchers.IO) {
            requireLocal()
            val (name, bytes) = activity.readDocument(document, if (script) 128 * 1024 else 1024 * 1024)
            if (script) mapOf("name" to name, "source" to Charsets.UTF_8.newDecoder().decode(ByteBuffer.wrap(bytes)).toString())
            else mapOf("name" to name, "dataType" to "base64", "data" to Base64.encodeToString(bytes, Base64.NO_WRAP),
                "headers" to listOf(mapOf("name" to "Content-Type", "value" to (activity.contentResolver.getType(document) ?: "application/octet-stream"))))
        }
    }

    private suspend fun exportCertificate(module: Boolean): Boolean {
        val certificate = mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            certificates.exportCertificate()
        } }
        val name = if (module) certificate.moduleFilename else certificate.filename
        val destination = activity.pickDocument(name) ?: return false
        return mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            // Another Activity may rotate the CA while the document picker is open.
            certificates.requireCurrentExport(certificate)
            activity.contentResolver.openOutputStream(destination, "wt")?.use { output ->
                if (module) CaptureCertificateModule.write(output, certificate) { asset ->
                    activity.assets.open("${CaptureCertificateModule.assetDirectory}/$asset")
                } else output.write(certificate.pem.toByteArray(Charsets.US_ASCII))
            } ?: error("Unable to export the CA certificate or module")
            if (module) certificates.moduleExported(certificate)
            true
        } }
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
