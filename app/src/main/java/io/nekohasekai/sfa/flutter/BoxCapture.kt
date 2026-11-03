package io.nekohasekai.sfa.flutter

import android.content.Intent
import android.os.Build
import android.provider.Settings as AndroidSettings
import android.security.KeyChain
import android.util.Log
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import org.json.JSONArray
import java.io.File

/** Local routed HTTP recording and opt-in TLS interception. */
internal class BoxCapture(private val activity: BoxActivity) {
    private val directory = File(activity.noBackupFilesDir, "http-debug")
    private val captures = File(directory, "captures")
    private val settings = CaptureSettings(directory)
    private val certificate = MitmCertificate(activity)

    private companion object {
        val mutex = Mutex()
    }

    private fun requireLocal() {
        require(!CommandTarget.isRemote) { "HTTP debugging is only available for the local service" }
    }
    private fun status() = JSONObject(Libbox.readUICaptureStatus())

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        requireLocal()
        if (method == "capture.export") return export(args)
        if (method == "capture.ca.export" || method == "capture.ca.module") return exportCertificate(method, args)
        if (method == "capture.ca.install") return installCertificate(args)
        return mutex.withLock {
            var started = false
            try {
                withContext(Dispatchers.IO) {
                    requireLocal()
                    if (!method.startsWith("capture.ca.")) Libbox.openUICapture(captures.absolutePath)
                    when (method) {
                        "capture.ca.snapshot" -> certificate.snapshot() + mapOf(
                            "captureActive" to status().optBoolean("active"),
                            "mitmActive" to status().optBoolean("mitmActive"),
                        )
                        "capture.ca.generate" -> {
                            require(!status().optBoolean("active")) { "Stop capture before changing the MITM certificate" }
                            certificate.generate(args["revision"] as? String ?: error("Missing certificate revision"))
                        }
                        "capture.snapshot" -> mapOf(
                            "state" to jsonObject(status()),
                            "page" to jsonObject(JSONObject(Libbox.listUICapture(
                                (args["limit"] as? Number)?.toInt()?.coerceIn(1, 300) ?: 50,
                                (args["query"] as? String).orEmpty().take(1024),
                            ))),
                        )
                        "capture.settings" -> settings.snapshot()
                        "capture.configure" -> {
                            require(!status().optBoolean("active")) { "Stop capture before changing its settings" }
                            @Suppress("UNCHECKED_CAST")
                            val value = args["config"] as? Map<String, Any?> ?: error("Missing capture settings")
                            settings.save(args["revision"] as? String ?: error("Missing capture settings revision"), JSONObject(value).toString())
                        }
                        "capture.start" -> start { started = true }
                        "capture.stop" -> { Libbox.stopUICapture(); jsonObject(status()) }
                        "capture.clear" -> { Libbox.clearUICapture(); null }
                        "capture.domains" -> {
                            val domains = JSONArray(Libbox.extractUICaptureHosts((args["query"] as? String).orEmpty().take(1024)))
                            List(domains.length()) { domains.getString(it) }
                        }
                        "capture.detail" -> jsonObject(JSONObject(Libbox.readUICapture(args["id"] as? String ?: error("Missing request ID"))))
                        else -> error("Unknown capture operation")
                    }
                }
            } catch (error: Exception) {
                // Covers cancellation while returning from Dispatchers.IO as
                // well as failures after the native session has been created.
                if (started) withContext(NonCancellable + Dispatchers.IO) {
                    runCatching { Libbox.stopUICapture() }.onFailure(error::addSuppressed)
                }
                if (method == "capture.start" && error !is CancellationException) {
                    Log.e("BoxCapture", "Capture startup failed", error)
                }
                throw error
            }
        }
    }

    private suspend fun start(onStarted: () -> Unit): Map<String, Any?> {
        require(!status().optBoolean("active")) { "Capture is already running" }
        require(activity.serviceStatus.value == Status.Started) { "Start the local service before capturing requests" }
        val running = BoxService.dashboardConfig.value ?: error("Local service is not running")
        val value = settings.config()
        return ProfileContent.mutex.withLock {
            requireLocal()
            require(activity.serviceStatus.value == Status.Started &&
                running.profileId == Settings.selectedProfile &&
                running === BoxService.dashboardConfig.value && running.server === BoxService.dashboardServer) {
                "Local service changed; start capture again"
            }
            synchronized(MitmCertificate.accessLock) {
                running.server.startUICapture(captures.absolutePath, value, "", null)
            }
            onStarted()
            currentCoroutineContext().ensureActive()
            requireLocal()
            val state = status()
            check(state.optBoolean("active")) { state.optString("error", "Capture startup failed") }
            jsonObject(state)
        }
    }

    private suspend fun installCertificate(args: Map<String, Any?>): Boolean = mutex.withLock {
        val ca = withContext(Dispatchers.IO) {
            requireLocal()
            certificate.publicCertificate(args["revision"] as? String ?: error("Missing certificate revision"))
        }
        withContext(Dispatchers.Main) {
            requireLocal()
            // Android 11+ requires the user to select the exported CA in Settings.
            val intent = if (Build.VERSION.SDK_INT >= 30) Intent(AndroidSettings.ACTION_SECURITY_SETTINGS)
                else KeyChain.createInstallIntent().putExtra(KeyChain.EXTRA_CERTIFICATE, ca.encoded)
                    .putExtra(KeyChain.EXTRA_NAME, ca.subjectX500Principal.name)
            activity.startActivity(intent)
        }
        true // Only confirms that settings opened, never that trust was granted.
    }

    private suspend fun exportCertificate(method: String, args: Map<String, Any?>): Boolean {
        val revision = args["revision"] as? String ?: error("Missing certificate revision")
        val ca = mutex.withLock { withContext(Dispatchers.IO) { requireLocal(); certificate.publicCertificate(revision) } }
        val module = method == "capture.ca.module"
        val name = "B-box-MITM-${MitmCertificate.fingerprint(ca).take(12)}" + if (module) ".zip" else ".crt"
        val destination = activity.pickDocument(name) ?: return false
        return mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            certificate.publicCertificate(revision)
            activity.contentResolver.openOutputStream(destination, "wt")?.use { output ->
                if (module) MitmCertificateModule.write(activity, output, ca)
                else output.write(MitmCertificate.pem(ca).toByteArray(Charsets.US_ASCII))
            } ?: error("Unable to export MITM certificate")
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
