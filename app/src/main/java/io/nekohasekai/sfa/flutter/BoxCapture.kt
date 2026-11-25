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
import java.io.Closeable
import java.util.UUID

/** Local routed HTTP recording and opt-in TLS interception. */
internal class BoxCapture(private val activity: BoxHost) : Closeable {
    private val directory = File(activity.noBackupFilesDir, "http-debug")
    private val captures = File(directory, "captures")
    private val settings = CaptureSettings(directory)
    private val certificate = MitmCertificate(activity)
    private val bodyReaders = CaptureBodyReaders(File(activity.cacheDir, "capture-body-views"))
    private val injectTasks = CaptureInjectTasks()
    private val replayTasks = CaptureReplayTasks()
    private val captureTasks = CaptureTasks(bodyReaders, injectTasks, replayTasks)

    private companion object {
        val mutex = Mutex()
    }

    private fun requireLocal() {
        require(!CommandTarget.isRemote) { "HTTP debugging is only available for the local service" }
    }
    private fun status() = JSONObject(Libbox.readUICaptureStatus())

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        // Cleanup must also work after the UI has switched to a remote session.
        if (method == "capture.body.close") return withContext(Dispatchers.IO) {
            bodyReaders.closeReader(args["readerId"] as? String ?: error("Missing body reader ID"))
        }
        if (method == "capture.inject.cancel") return injectTasks.cancel(args["requestId"] as? String ?: error("Missing injection request ID"))
        if (method == "capture.replay.cancel") return replayTasks.cancel(args["requestId"] as? String ?: error("Missing replay request ID"))
        requireLocal()
        if (method == "capture.replay") return replay(args)
        if (method.startsWith("capture.breakpoint")) return breakpoints(method, args)
        if (method == "capture.inject") {
            val opcode = args["opcode"]?.let { raw ->
                val value = raw as? Number ?: error("Invalid stream message opcode")
                require(value.toDouble() in setOf(0.0, 1.0, 2.0)) { "Invalid stream message opcode" }
                value.toInt()
            } ?: 0
            return injectTasks.inject(
                args["requestId"] as? String ?: UUID.randomUUID().toString(),
                args["streamId"] as? String ?: error("Missing stream ID"),
                args["direction"] as? String ?: error("Missing stream direction"),
                opcode,
                args["data"] as? String ?: error("Missing stream message"),
            )
        }
        if (method in setOf("capture.body", "capture.body.view", "capture.body.view.page")) return readBody(method, args)
        if (method in setOf("capture.snapshot", "capture.query", "capture.detail", "capture.domains")) return readCapture(method, args)
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
                        "capture.settings" -> settings.snapshot()
                        "capture.configure" -> {
                            require(!status().optBoolean("active")) { "Stop capture before changing its settings" }
                            @Suppress("UNCHECKED_CAST")
                            val value = args["config"] as? Map<String, Any?> ?: error("Missing capture settings")
                            settings.save(args["revision"] as? String ?: error("Missing capture settings revision"), JSONObject(value).toString())
                        }
                        "capture.start" -> start { started = true }
                        "capture.stop" -> { Libbox.stopUICapture(); jsonObject(status()) }
                        "capture.clear" -> { captureTasks.clearRecords { Libbox.clearUICapture() }; null }
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

    private suspend fun readCapture(method: String, args: Map<String, Any?>): Any? {
        val session = CommandTarget.localSession()
        return withContext(Dispatchers.IO) {
            CommandTarget.requireLocalSession(session)
            Libbox.openUICapture(captures.absolutePath)
            val result = when (method) {
                "capture.snapshot" -> mapOf(
                    "state" to jsonObject(status()),
                    "page" to jsonObject(JSONObject(Libbox.listUICapture(
                        (args["limit"] as? Number)?.toInt()?.also { require(it >= 0) { "Invalid page size" } } ?: 50,
                        (args["query"] as? String).orEmpty(),
                    ))),
                )
                "capture.query" -> mapOf(
                    "state" to jsonObject(status()),
                    "page" to jsonObject(JSONObject(Libbox.queryUICapture(JSONObject(args).toString()))),
                )
                "capture.detail" -> jsonObject(JSONObject(Libbox.readUICapture(args["id"] as? String ?: error("Missing request ID"))))
                "capture.domains" -> {
                    val domains = JSONArray(Libbox.extractUICaptureHosts((args["query"] as? String).orEmpty()))
                    List(domains.length()) { domains.getString(it) }
                }
                else -> error("Unknown capture read operation")
            }
            CommandTarget.requireLocalSession(session)
            result
        }
    }

    private suspend fun readBody(method: String, args: Map<String, Any?>): Any? {
        val session = CommandTarget.localSession()
        withContext(Dispatchers.IO) {
            CommandTarget.requireLocalSession(session)
            Libbox.openUICapture(captures.absolutePath)
            CommandTarget.requireLocalSession(session)
        }
        val readerId = args["readerId"] as? String ?: if (method == "capture.body") UUID.randomUUID().toString()
            else error("Missing body reader ID")
        try {
            val value = if (method == "capture.body.view.page") {
                val offset = (args["offset"] as? Number)?.toLong() ?: error("Missing body offset")
                require(offset >= 0) { "Invalid body offset" }
                bodyReaders.readView(readerId, offset)
            } else {
                val direction = args["direction"] as? String
                require(direction == "request" || direction == "response") { "Invalid body direction" }
                val id = args["id"] as? String ?: error("Missing request ID")
                if (method == "capture.body.view") {
                    bodyReaders.prepareView(readerId, id, direction == "response", args["format"] as? String ?: error("Missing body view format"))
                } else {
                    val offset = (args["offset"] as? Number)?.toLong() ?: error("Missing body offset")
                    require(offset >= 0) { "Invalid body offset" }
                    val source = args["source"] as? String ?: "decoded"
                    require(source == "decoded" || source == "raw") { "Invalid body source" }
                    bodyReaders.read(readerId, id, direction == "response", offset, source == "raw")
                }
            }
            CommandTarget.requireLocalSession(session)
            return jsonObject(JSONObject(value))
        } finally {
            if (!args.containsKey("readerId")) withContext(NonCancellable + Dispatchers.IO) { bodyReaders.closeReader(readerId) }
        }
    }

    /** The record is read from the saved history; the send belongs to the running service instance. */
    private suspend fun replay(args: Map<String, Any?>): Any? {
        val id = args["id"] as? String ?: error("Missing request ID")
        val options = args["options"]?.let { it as? Map<*, *> ?: error("Invalid replay options") } ?: emptyMap<Any?, Any?>()
        require(activity.serviceStatus.value == Status.Started) { "Start the local service before replaying a request" }
        val session = CommandTarget.localSession()
        withContext(Dispatchers.IO) {
            CommandTarget.requireLocalSession(session)
            Libbox.openUICapture(captures.absolutePath)
            CommandTarget.requireLocalSession(session)
        }
        return jsonObject(JSONObject(replayTasks.replay(
            args["requestId"] as? String ?: UUID.randomUUID().toString(), id, JSONObject(options).toString())))
    }

    /** Rules and held messages are process state of the local core; nothing here is saved. */
    private suspend fun breakpoints(method: String, args: Map<String, Any?>): Any? {
        val session = CommandTarget.localSession()
        return withContext(Dispatchers.IO) {
            CommandTarget.requireLocalSession(session)
            val result: Any? = when (method) {
                "capture.breakpoints" -> jsonObject(JSONObject(Libbox.readUICaptureBreakpoints()))
                "capture.breakpoints.set" -> {
                    val config = args["config"] as? Map<*, *> ?: error("Missing breakpoint settings")
                    jsonObject(JSONObject(Libbox.setUICaptureBreakpoints(JSONObject(config).toString())))
                }
                "capture.breakpoint.detail" -> jsonObject(JSONObject(
                    Libbox.readUICapturePausedMessage(args["id"] as? String ?: error("Missing paused message ID"))))
                "capture.breakpoint.resolve" -> {
                    val decision = args["decision"] as? Map<*, *> ?: error("Missing breakpoint decision")
                    Libbox.resolveUICaptureBreakpoint(args["id"] as? String ?: error("Missing paused message ID"), JSONObject(decision).toString())
                    true
                }
                else -> error("Unknown breakpoint operation")
            }
            CommandTarget.requireLocalSession(session)
            result
        }
    }

    fun cancelBodyReads() = captureTasks.cancelAll()

    override fun close() = captureTasks.close()

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
            activity.openActivity(intent)
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
        // Resolve the entire filter before opening the picker. Only the native
        // side holds these IDs; unloaded rows remain searchable and exportable.
        val selection = if (args["format"] == "har" && args.containsKey("selection")) {
            mutex.withLock { withContext(Dispatchers.IO) {
                requireLocal()
                Libbox.openUICapture(captures.absolutePath)
                val query = args["selection"] as? Map<*, *> ?: error("Invalid HAR query")
                Libbox.selectUICaptureHAR(JSONObject(query).toString())
            } }
        } else null
        val destination = activity.pickDocument(name) ?: return false
        requireLocal()
        return mutex.withLock { withContext(Dispatchers.IO) {
            requireLocal()
            Libbox.openUICapture(captures.absolutePath)
            check(directory.isDirectory || directory.mkdirs()) { "Unable to create capture directory" }
            val file = File.createTempFile("export-", ".tmp", directory)
            try {
                when {
                    selection != null -> Libbox.exportUICaptureHARSelection(file.absolutePath, selection)
                    args["format"] == "curl" -> Libbox.exportUICaptureCurlFile(file.absolutePath, args["id"] as? String ?: error("Missing request ID"))
                    args["format"] == "domains" -> {
                        val domains = JSONArray(Libbox.extractUICaptureHosts((args["query"] as? String).orEmpty()))
                        file.writeText(List(domains.length()) { domains.getString(it) }.joinToString("\n", postfix = "\n"))
                    }
                    args.containsKey("ids") -> {
                        val ids = args["ids"] as? List<*> ?: error("Missing HAR request selection")
                        require(ids.all { it is String }) { "Invalid HAR request selection" }
                        Libbox.exportUICaptureHARSelection(file.absolutePath, JSONArray(ids).toString())
                    }
                    else -> Libbox.exportUICaptureHAR(file.absolutePath, (args["query"] as? String).orEmpty())
                }
                activity.contentResolver.openOutputStream(destination, "wt")?.use { output ->
                    file.inputStream().use { it.copyTo(output) }
                } ?: error("Unable to export capture")
                true
            } finally { file.delete() }
        } }
    }
}
