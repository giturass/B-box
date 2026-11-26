package io.nekohasekai.sfa.flutter

import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.net.Uri
import android.os.PowerManager
import android.util.Log
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import com.google.android.gms.tasks.Tasks
import com.google.mlkit.vision.barcode.BarcodeScannerOptions
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.EventChannel
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.StatusMessage
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.BuildConfig
import io.nekohasekai.sfa.backend.base.GlobalEventBus
import io.nekohasekai.sfa.backend.base.UiEvent
import io.nekohasekai.sfa.backend.connections.ConnectionsViewModel
import io.nekohasekai.sfa.backend.dashboard.DashboardViewModel
import io.nekohasekai.sfa.backend.dashboard.groups.GroupsViewModel
import io.nekohasekai.sfa.backend.log.LogViewModel
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.RemoteServer
import io.nekohasekai.sfa.database.RemoteServerManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.CommandTarget
import io.nekohasekai.sfa.utils.HTTPClient
import io.nekohasekai.sfa.ktx.unwrap
import io.nekohasekai.sfa.utils.RemoteControlManager
import io.nekohasekai.sfa.vendor.PackageQueryManager
import io.nekohasekai.sfa.vendor.PerAppProxyScanner
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.Closeable
import java.io.IOException

internal class BoxBridge(private val activity: BoxHost, messenger: BinaryMessenger) : Closeable {
    private val scope = activity.lifecycleScope
    private val channel = MethodChannel(messenger, "io.ericlee.bfa/box")
    private val events = EventChannel(messenger, "io.ericlee.bfa/events")
    private var sink: EventChannel.EventSink? = null
    private var streamJob: Job? = null
    private val queuedErrors = ArrayDeque<String>()
    private lateinit var dashboard: DashboardViewModel
    private lateinit var groups: GroupsViewModel
    private lateinit var connections: ConnectionsViewModel
    private lateinit var logs: LogViewModel
    internal lateinit var toolBridge: BoxTools
    private lateinit var dashboardDetails: BoxDashboard
    private val connectionsVisible = MutableStateFlow(false)
    private val logsVisible = MutableStateFlow(false)
    private val operations = Mutex()
    private val calls = mutableSetOf<Job>()
    private var quiescing = false
    private var batteryRequestPending = false
    private var profileCallbackRegistered = false
    private var packageListenersRegistered = false
    private val apps = AppListCache {
        val packageManager = activity.packageManager
        PackageQueryManager.getInstalledPackages(android.content.pm.PackageManager.GET_PERMISSIONS).mapNotNull { info ->
            if (info.packageName == activity.packageName) return@mapNotNull null
            val application = info.applicationInfo ?: return@mapNotNull null
            AppListCache.Entry(
                packageName = info.packageName,
                system = application.flags and android.content.pm.ApplicationInfo.FLAG_SYSTEM != 0,
                internet = info.requestedPermissions?.contains(android.Manifest.permission.INTERNET) == true,
                updatedAt = info.lastUpdateTime,
                loadLabel = { application.loadLabel(packageManager).toString() },
            )
        }
    }
    private val reports = BoxReports(activity)
    private val profiles = BoxProfiles(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) {
        recover -> withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
    }
    private val nodes = BoxNodes(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) {
        recover -> withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
    }
    private val warpProfiles = BoxWarpProfiles(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) {
        recover -> withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
    }
    private val warp = BoxWarp(
        prepare = { id, revision, session -> localProfiles(session) { warpProfiles.prepare(id, revision, session) } },
        commit = ::commitWarp,
    )
    private val subscriptions = BoxNodeSubscriptions(beforeApply = { ProfileContent.requireStableService(activity.serviceStatus.value) }) {
        recover -> withContext(Dispatchers.Main) { activity.applyProfile(recover = recover) }
    }
    private val capture = BoxCapture(activity)
    private val scripts = BoxScripts(
        prepare = { request, created ->
            val session = CommandTarget.localSession()
            localProfiles(session) { prepareProfileScript(request, session, created) }
        },
        validate = { target: ScriptDebugProfile -> localProfiles(target.session) { validateProfileScript(target) } },
    )
    private val qrScanner = BoxQrScanner(profiles::decode)
    private val preferences = activity.getSharedPreferences("flclash_ui", 0)
    private val webdav = BoxWebDAV(activity)
    private val backups = BoxBackups(activity, ::settings, ::patchSettings,
        { restart, recover -> withContext(Dispatchers.Main) { activity.applyProfile(restart, recover) } }, ::publishNodes, ::emit, webdav)
    private val rates = object : CommandClient.Handler {
        override fun updateStatus(status: StatusMessage) {
            if (::dashboardDetails.isInitialized) dashboardDetails.remoteMemory = status.memory
            emit("rates", mapOf("upload" to status.uplink, "download" to status.downlink, "memoryBytes" to status.memory))
        }
    }
    private val profileCallback: () -> Unit = { scope.launch { operations.withLock { publishProfiles(ensureDefault = false) } } }
    private val initialize = scope.async {
        activity.initialize()
        withContext(Dispatchers.IO) {
            Settings.dataStore.initialize()
            Application.application.setupLibbox()
            profiles.ensureDefault()
        }
        val provider = ViewModelProvider(activity)
        dashboard = provider[DashboardViewModel::class.java]
        groups = provider[GroupsViewModel::class.java]
        connections = provider[ConnectionsViewModel::class.java]
        logs = provider[LogViewModel::class.java]
        toolBridge = BoxTools(activity, ::emit)
        dashboardDetails = BoxDashboard(activity, ::emit)
        dashboard.commandClient.addHandler(rates)
        ProfileManager.registerCallback(profileCallback)
        profileCallbackRegistered = true
        PackageQueryManager.registerListeners()
        packageListenersRegistered = true
        RemoteControlManager.restore()
        scope.launch {
            activity.serviceStatus.collect {
                dashboard.updateServiceStatus(it)
                groups.updateServiceStatus(it)
                connections.updateServiceStatus(it)
                logs.updateServiceStatus(it)
                if (it == Status.Stopped) {
                    emit("rates", mapOf("upload" to 0L, "download" to 0L))
                    scope.launch {
                        operations.withLock { emit("nodes", withContext(Dispatchers.IO) { nodeState() }) }
                    }
                }
            }
        }
        scope.launch {
            GlobalEventBus.events.collect { event ->
                when (event) {
                    is UiEvent.ErrorMessage -> reportError(event.message)
                }
            }
        }
        scope.launch {
            var previousConfig = BoxService.dashboardConfig.value
            var previousRemote = RemoteControlManager.remoteServer.value?.id
            combine(activity.serviceStatus, RemoteControlManager.remoteServer, BoxService.dashboardConfig, BoxService.processingApplying) {
                status, remote, config, applying -> ScriptServiceState(status, remote?.id, config, applying)
            }.collect { state ->
                if (state.status != Status.Started || state.remoteId != null || state.applying ||
                    state.config !== previousConfig || state.remoteId != previousRemote) {
                    scripts.cancelAll()
                }
                if (state.remoteId != previousRemote) capture.cancelBodyReads()
                previousConfig = state.config
                previousRemote = state.remoteId
                emit("dashboard", dashboard.uiState.value.toWire())
            }
        }
    }

    init {
        channel.setMethodCallHandler { call, result ->
            if (quiescing && call.method != "intent.complete") {
                result.error("cancelled", "UI is closing", null)
                return@setMethodCallHandler
            }
            lateinit var job: Job
            job = scope.launch(start = CoroutineStart.LAZY) {
                try {
                    initialize.await()
                    result.success(invoke(call))
                } catch (e: CancellationException) {
                    result.error("cancelled", "Operation cancelled", null)
                } catch (e: BoxWarpException) {
                    result.error(e.code, e.message, e.details)
                } catch (e: Exception) {
                    result.error("box_error", e.message ?: e.toString(), null)
                } finally {
                    calls.remove(job)
                }
            }
            calls.add(job)
            job.start()
        }
        events.setStreamHandler(object : EventChannel.StreamHandler {
            override fun onListen(arguments: Any?, eventSink: EventChannel.EventSink) {
                sink = eventSink
                streamJob?.cancel()
                streamJob = scope.launch {
                    try {
                        initialize.await()
                        toolBridge.synchronizeEvents()
                        while (queuedErrors.isNotEmpty()) emit("error", queuedErrors.removeFirst())
                        coroutineScope {
                            launch { dashboardDetails.synchronizeNetwork() }
                            launch { dashboard.uiState.collect { emit("dashboard", it.toWire()) } }
                            launch { groups.uiState.collect { emit("groups", it.groups.map { group -> group.toWire() }) } }
                            launch { BoxUpdates.state.collect { emit("updates", BoxUpdates.snapshot()) } }
                            launch {
                                val stream = ConnectionStream()
                                combine(connections.uiState, connectionsVisible, AppLifecycleObserver.isForeground) { state, visible, foreground ->
                                    state.takeIf { visible && foreground }
                                }.collect { state ->
                                    if (state == null) stream.reset()
                                    else {
                                        val update = withContext(Dispatchers.Default) { stream.update(state.allConnections) }
                                        if (update != null) emit("connections", update)
                                    }
                                }
                            }
                            launch {
                                val stream = LogStream()
                                combine(logs.uiState, logsVisible, AppLifecycleObserver.isForeground) { state, visible, foreground ->
                                    state.takeIf { visible && foreground }
                                }.collect { state ->
                                    if (state == null) stream.reset()
                                    else {
                                        val update = withContext(Dispatchers.Default) { stream.update(state) }
                                        if (update != null) emit("logs", update)
                                    }
                                }
                            }
                            launch { RemoteControlManager.remoteServer.collect { emit("dashboard", dashboard.uiState.value.toWire()) } }
                            launch { RemoteControlManager.isConnected.collect { emit("dashboard", dashboard.uiState.value.toWire()) } }
                        }
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        reportError(e.message ?: e.toString())
                    }
                }
            }

            override fun onCancel(arguments: Any?) {
                sink = null
                streamJob?.cancel()
                logsVisible.value = false
                if (::toolBridge.isInitialized) {
                    toolBridge.setVisible(false)
                    toolBridge.setOutboundsVisible(false)
                }
                if (::dashboardDetails.isInitialized) {
                    dashboardDetails.setMemoryVisible(false)
                    dashboardDetails.setLanVisible(false)
                }
                if (::connections.isInitialized && connectionsVisible.value) {
                    connectionsVisible.value = false
                    connections.setVisible(false)
                }
            }
        })
    }

    private suspend fun invoke(call: MethodCall): Any? {
        @Suppress("UNCHECKED_CAST")
        val args = (call.arguments as? Map<String, Any?>).orEmpty()
        val method = call.method
        // The tool bridge owns every name that is not handled here, and rejects unknown ones.
        return when (method.substringBefore('.', "")) {
            "dashboard" -> invokeDashboard(method, args)
            "service" -> invokeService(method, args)
            "profiles" -> invokeProfiles(method, args)
            "nodes" -> invokeNodes(method, args)
            "warp" -> invokeWarp(method, args)
            "subscriptions" -> invokeSubscriptions(method, args)
            "scripts" -> invokeScripts(method, args)
            "capture" -> invokeCapture(method, args)
            "groups", "mode", "systemProxy" -> invokeProxy(method, args)
            "connections" -> invokeConnections(method, args)
            "logs" -> invokeLogs(method, args)
            "preferences", "settings" -> invokeSettings(method, args)
            "apps" -> invokeApps(method, args)
            "remote" -> invokeRemote(method, args)
            "backup", "webdav" -> invokeBackup(method, args)
            "reports" -> reports.invoke(method, args)
            "updates" -> invokeUpdates(method, args)
            else -> invokeGeneral(method, args)
        }
    }

    private suspend fun invokeScripts(method: String, args: Map<String, Any?>): Any? = when (method) {
        "scripts.run" -> scripts.run(ScriptRunRequest.read(args))
        "scripts.cancel" -> scripts.cancel(args.string("requestId"))
        else -> error("Unknown script operation")
    }

    private suspend fun invokeUpdates(method: String, args: Map<String, Any?>): Any? = when (method) {
        "updates.check" -> withContext(Dispatchers.IO) { BoxUpdates.check() }
        "updates.snapshot" -> BoxUpdates.snapshot()
        "updates.download" -> BoxUpdates.download(args.long("versionCode"))
        "updates.cancel" -> BoxUpdates.cancel()
        "updates.install" -> BoxUpdates.install(args.long("versionCode"), activity::installUpdate)
        else -> error("Unknown update operation")
    }

    private suspend fun invokeWarp(method: String, args: Map<String, Any?>): Any? = try {
        when (method) {
            "warp.snapshot" -> warp.snapshot()
            "warp.register" -> warp.register(args.string("requestId"), args.long("profileId"), args.string("profileRevision"),
                args.string("name"), args.string("transport"))
            "warp.retry" -> warp.retry(args.string("pendingId"), args.long("profileId"), args.string("profileRevision"))
            "warp.discard" -> { warp.discard(args.string("pendingId")); true }
            "warp.cancel" -> warp.cancel(args.string("requestId"))
            else -> throw BoxWarpException("warp_error", "Unknown WARP operation")
        }
    } catch (error: CancellationException) {
        throw error
    } catch (error: BoxWarpException) {
        throw error
    } catch (_: Exception) {
        throw BoxWarpException("warp_error", "Select a local profile and refresh it before registering WARP")
    }

    private suspend fun commitWarp(target: WarpProfileSnapshot, node: String): Map<String, Any?> = localProfiles(target.session) {
        var saved = false
        try {
            warpProfiles.commit(target, node).also { saved = true }
        } finally {
            withContext(NonCancellable) {
                try {
                    publishProfiles(ensureDefault = saved)
                } catch (_: Exception) {
                    // The confirmed file/service result does not depend on publishing a UI snapshot.
                    reportError("Configuration status could not be refreshed")
                }
            }
        }
    }

    private suspend fun invokeGeneral(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "ready" -> return ready()
            "intent.take" -> return activity.takeIntent()
            "intent.complete" -> activity.completeIntent(args.string("id"))
            "config.check" -> return withContext(Dispatchers.IO) {
                BoxProfiles.validate(args.string("content"))
                true
            }
            "config.format" -> return withContext(Dispatchers.IO) { Libbox.formatConfig(args.string("content")).value }
            "document.save" -> return saveDocument(args.string("name"), args.string("content"))
            "share.text" -> {
                val send = Intent(Intent.ACTION_SEND).apply {
                    type = "text/plain"
                    putExtra(Intent.EXTRA_TEXT, args.string("content"))
                }
                activity.openActivity(Intent.createChooser(send, null))
            }
            "battery.status" -> return batteryExempt()
            "battery.open" -> {
                val exempt = batteryExempt()
                if (exempt || batteryRequestPending) return exempt
                batteryRequestPending = true
                try {
                    activity.requestIgnoreBatteryOptimization()
                    return batteryExempt()
                } finally { batteryRequestPending = false }
            }
            "url.open" -> {
                val uri = Uri.parse(args.string("url"))
                require(uri.scheme in listOf("http", "https")) { "Unsupported URL" }
                activity.openActivity(Intent(Intent.ACTION_VIEW, uri))
            }
            "terminal.set", "terminal.importFont", "terminal.deleteFont" -> return operations.withLock { toolBridge.invoke(method, args) }
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun ready(): Map<String, Any?> = coroutineScope {
        val initialProfiles = async(Dispatchers.IO) { profiles.list() }
        val initialSettings = async(Dispatchers.IO) { settings() }
        val initialPreferences = async(Dispatchers.IO) { readPreferences() }
        val initialNodes = async(Dispatchers.IO) { nodeState() }
        // The dashboard and the pending intent are read only after the stored state has loaded.
        val profileList = initialProfiles.await()
        val settingValues = initialSettings.await()
        val preferenceValues = initialPreferences.await()
        mapOf(
            "version" to BuildConfig.VERSION_NAME,
            "coreVersion" to Libbox.version(),
            "updates" to BoxUpdates.snapshot(),
            "systemAccentColor" to systemAccentColor(),
            "dashboard" to dashboard.uiState.value.toWire(),
            "profiles" to profileList,
            "nodes" to initialNodes.await(),
            "settings" to settingValues,
            "preferences" to preferenceValues,
            "intent" to activity.takeIntent(),
        )
    }

    private suspend fun saveDocument(name: String, content: String): Boolean {
        val uri = activity.pickDocument(name) ?: return false
        withContext(Dispatchers.IO) {
            activity.contentResolver.openOutputStream(uri, "wt")?.use { it.write(content.toByteArray()) }
                ?: error("Unable to write document")
        }
        return true
    }

    private suspend fun invokeDashboard(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "dashboard.egress" -> return dashboardDetails.egress(args.string("id"))
            "dashboard.probe.open" -> dashboardDetails.openProbe(args.string("id"))
            "dashboard.probe.get" -> return dashboardDetails.query(args.string("id"), args.string("url"), args.long("timeoutMillis"))
            "dashboard.probe.close" -> dashboardDetails.closeProbe(args.string("id"))
            "dashboard.qualityCache.get" -> return dashboardDetails.readQualityCache(args.string("ip"))
            "dashboard.qualityCache.put" -> {
                val record = args.objectOrNull("value") ?: error("Missing IP quality record")
                dashboardDetails.writeQualityCache(args.string("ip"), record)
            }
            "dashboard.releaseMemory" -> return dashboardDetails.releaseMemory()
            "dashboard.memory.visible" -> dashboardDetails.setMemoryVisible(args["visible"] == true)
            "dashboard.lan.visible" -> dashboardDetails.setLanVisible(args["visible"] == true)
            "dashboard.lan.refresh" -> return dashboardDetails.refreshNetwork(force = true)
            "dashboard.lan.set" -> return setLanProxy(args)
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun setLanProxy(args: Map<String, Any?>): Map<String, Any?> = localProfiles {
        val enabled = args["enabled"] as? Boolean ?: error("Missing LAN proxy state")
        val port = args["port"]?.let {
            val number = it as? Number ?: error("Invalid proxy port")
            require(number.toDouble() == number.toInt().toDouble() && number.toInt() in 1..65535) { "Invalid proxy port" }
            number.toInt()
        } ?: 0
        profiles.setLanProxy(args.long("profileId"), args.string("revision"), enabled, port)
        publishProfiles()
        dashboardDetails.refreshNetwork()
    }

    private suspend fun invokeService(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "service.start" -> {
                require(!CommandTarget.isRemote) { "Exit remote control before starting the local service" }
                activity.startService()
            }
            "service.stop" -> {
                require(!CommandTarget.isRemote) { "Exit remote control before stopping the local service" }
                activity.stopService()
            }
            "service.reload" -> operations.withLock {
                if (CommandTarget.isRemote) {
                    withContext(Dispatchers.IO) { CommandTarget.standaloneClient().serviceReload() }
                } else {
                    activity.applyProfile(args["restart"] == true)
                }
            }
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun invokeProfiles(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "profiles.read" -> return withContext(Dispatchers.IO) { profiles.read(args.id()) }
            "profiles.config" -> return withContext(Dispatchers.IO) { profiles.config(args.id()) }
            "profiles.stats" -> return withContext(Dispatchers.IO) { profiles.stats(args.id()) }
            "profiles.write" -> return changeProfiles { profiles.writeContent(args.id(), args.string("content"), args.string("revision")) }
            "profiles.select" -> changeProfiles { profiles.select(args.id()) { activity.serviceStatus.value } }
            "profiles.save" -> return saveProfile(args)
            "profiles.update" -> updateProfile(args)
            "profiles.delete" -> changeProfiles { profiles.delete(args.id()) }
            "profiles.reorder" -> changeProfiles { profiles.reorder((args["ids"] as List<*>).map { (it as Number).toLong() }) }
            "profiles.importFile" -> return importProfile()
            "profiles.moduleFile", "profiles.moduleFetch", "profiles.moduleDescribe", "profiles.moduleDetails" -> return moduleInput(method, args)
            "profiles.importResource" -> return importResource(args)
            "profiles.scan.image" -> return scanImage()
            "profiles.scan" -> return withContext(Dispatchers.Default) { qrScanner.scan(args.string("session"), args.string("text")) }
            "profiles.scan.close" -> qrScanner.close(args.string("session"))
            "profiles.decodeIntent" -> return decodeIntent(args.string("uri"))
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun saveProfile(args: Map<String, Any?>): Long {
        val session = CommandTarget.localSession()
        // Validate supplied configuration content before entering the operation lock.
        val prepared = withContext(Dispatchers.IO) { profiles.prepareSave(args) }
        return changeProfiles(session) { profiles.savePrepared(prepared) { CommandTarget.requireLocalSession(session) } }
    }

    private suspend fun updateProfile(args: Map<String, Any?>) {
        val session = CommandTarget.localSession()
        val snapshot = localProfiles(session) { profiles.prepareUpdate(args.id()) }
        // The subscription downloads between the two locked steps.
        val content = withContext(Dispatchers.IO) { profiles.downloadUpdate(snapshot) }
        changeProfiles(session) { profiles.commitUpdate(snapshot, content) { CommandTarget.requireLocalSession(session) } }
    }

    private suspend fun importProfile(): Map<String, Any?>? {
        val uri = activity.pickDocument() ?: return null
        return withContext(Dispatchers.IO) { decodeDocument(uri) }
    }

    private suspend fun moduleInput(method: String, args: Map<String, Any?>): Any? {
        val session = CommandTarget.localSession()
        val result = when (method) {
            "profiles.moduleFile" -> {
                val uri = activity.pickDocument() ?: return null
                withContext(Dispatchers.IO) {
                    val bytes = activity.contentResolver.openInputStream(uri)?.use { input ->
                        val output = ByteArrayOutputStream()
                        val buffer = ByteArray(8192)
                        while (true) {
                            val count = input.read(buffer)
                            if (count < 0) break
                            require(output.size() + count <= 4 * 1024 * 1024) { "Module files must not exceed 4 MiB" }
                            output.write(buffer, 0, count)
                        }
                        output.toByteArray()
                    } ?: error("Unable to read document")
                    val name = activity.documentName(uri, "module.sgmodule").substringAfterLast('/').substringAfterLast('\\')
                    if (args["attachment"] == true) mapOf("name" to name, "base64" to android.util.Base64.encodeToString(bytes, android.util.Base64.NO_WRAP))
                    else mapOf("name" to name, "content" to bytes.decodeToString(throwOnInvalidSequence = true))
                }
            }
            "profiles.moduleFetch" -> {
                val url = BoxProfiles.validateUrl(args.string("url"))
                HTTPClient.withTask { it.moduleGet(url, HTTPClient.userAgent).unwrap }
            }
            "profiles.moduleDescribe" -> withContext(Dispatchers.Default) { Libbox.describeSurgeModule(args.string("module")) }
            else -> withContext(Dispatchers.IO) { profiles.moduleDetails(args.id(), args.string("revision"), args.string("moduleId")) }
        }
        CommandTarget.requireLocalSession(session)
        return result
    }

    private suspend fun scanImage(): List<String>? {
        val uri = activity.pickDocument(mimeType = "image/*") ?: return null
        return withContext(Dispatchers.IO) {
            val image = try {
                InputImage.fromFilePath(activity, uri)
            } catch (error: IOException) {
                throw IOException("Unable to read the selected image", error)
            }
            val options = BarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).build()
            BarcodeScanning.getClient(options).use { scanner ->
                Tasks.await(scanner.process(image)).mapNotNull { barcode ->
                    barcode.rawValue?.takeIf { it.isNotBlank() }
                }
            }
        }
    }

    private fun decodeDocument(uri: Uri): Map<String, Any?> {
        val (name, bytes) = activity.readDocument(uri)
        return try {
            profiles.decode(bytes, name)
        } catch (e: Exception) {
            // A document that is no configuration may hold proxy nodes.
            mapOf("nodes" to (nodes.detect(bytes.toString(Charsets.UTF_8)) ?: throw e))
        }
    }

    private suspend fun importResource(args: Map<String, Any?>): Map<String, Any?>? {
        val session = CommandTarget.localSession()
        val profileId = args.id()
        val section = args.string("section")
        val revision = args.string("revision")
        val uri = activity.pickDocument() ?: return null
        // Assigned while staging so that a cancelled copy still leaves the resource to close.
        var prepared: BoxProfiles.PreparedResource? = null
        return try {
            withContext(Dispatchers.IO) {
                CommandTarget.requireLocalSession(session)
                val name = activity.documentName(uri, "resource")
                activity.contentResolver.openInputStream(uri)?.use { input ->
                    prepared = profiles.prepareResource(profileId, section, revision, name, input)
                } ?: error("Unable to read resource document")
            }
            changeProfiles(session) { profiles.commitResource(checkNotNull(prepared)) { CommandTarget.requireLocalSession(session) } }
        } finally {
            withContext(NonCancellable + Dispatchers.IO) { prepared?.close() }
        }
    }

    private suspend fun decodeIntent(value: String): Map<String, Any?> {
        val uri = Uri.parse(value)
        return when {
            uri.scheme == "sing-box" && uri.host == "import-remote-profile" -> {
                val info = Libbox.parseRemoteProfileImportLink(value)
                mapOf("name" to info.name, "url" to info.url)
            }
            uri.scheme == "http" || uri.scheme == "https" -> {
                BoxProfiles.validateUrl(value)
                mapOf("name" to (uri.host ?: "Profile"), "url" to value)
            }
            else -> withContext(Dispatchers.IO) { decodeDocument(uri) }
        }
    }

    private suspend fun invokeSubscriptions(method: String, args: Map<String, Any?>): Any? {
        val session = CommandTarget.localSession()
        return when (method) {
            "subscriptions.list" -> localProfiles(session) { subscriptions.list(args.long("profileId")) }
            "subscriptions.import" -> {
                val prepared = localProfiles(session) { subscriptions.prepareImport(args) }
                val content = withContext(Dispatchers.IO) { subscriptions.downloadImport(prepared) }
                changeProfiles(session) { subscriptions.commitImport(prepared, content) { CommandTarget.requireLocalSession(session) } }
            }
            "subscriptions.update" -> {
                val snapshot = localProfiles(session) { subscriptions.prepareUpdate(args.long("profileId"), args.string("id")) }
                val content = withContext(Dispatchers.IO) { subscriptions.downloadUpdate(snapshot) }
                changeProfiles(session) { subscriptions.commitUpdate(snapshot, content) { CommandTarget.requireLocalSession(session) } }
            }
            "subscriptions.save" -> changeProfiles(session) { subscriptions.save(args) { CommandTarget.requireLocalSession(session) } }
            "subscriptions.delete" -> changeProfiles(session) {
                subscriptions.delete(args.long("profileId"), args.string("id")) { CommandTarget.requireLocalSession(session) }
            }
            else -> error("Unknown subscription operation")
        }
    }

    private suspend fun invokeNodes(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "nodes.parse" -> return withContext(Dispatchers.IO) { nodes.parse(args.string("content")) }
            "nodes.detect" -> return withContext(Dispatchers.IO) { nodes.detect(args.string("content")) }
            "nodes.clipboard" -> return clipboardNodes()
            "nodes.list" -> return localProfiles {
                if (args["profileId"] is Number) require(args.long("profileId") == Settings.selectedProfile) { "Selected profile changed; reload it before saving" }
                nodeState()
            }
            "nodes.get" -> return localProfiles { nodes.get(args.string("tag"), args.long("profileId")) }
            "nodes.share" -> {
                val session = CommandTarget.localSession()
                return localProfiles(session) {
                    nodes.share((args["tags"] as List<*>).map { it as String },
                        args.long("profileId"), args.string("profileRevision")).also {
                        CommandTarget.requireLocalSession(session)
                    }
                }
            }
            "nodes.add" -> return changeNodes { nodes.add(args.string("nodes"), args.long("profileId"), args.string("profileRevision")) }
            "nodes.set" -> changeNodes { nodes.set(args.string("tag"), args.string("content"), args["revision"] as? String,
                args.long("profileId"), args.string("profileRevision")) }
            "nodes.remove" -> changeNodes { nodes.remove((args["tags"] as List<*>).map { it as String },
                args.long("profileId"), args.string("profileRevision")) }
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    /** The node links copied since the last look, as libbox parsed them, or null. */
    private suspend fun clipboardNodes(): String? {
        // Android only permits the focused UI process to read the clipboard.
        val value = activity.readClipboard(preferences.getLong("clipboardStamp", 0L)) ?: return null
        val stamp = (value["stamp"] as? Number)?.toLong() ?: 0L
        if (stamp != 0L) preferences.edit().putLong("clipboardStamp", stamp).apply()
        val text = (value["text"] as? String).orEmpty().trim()
        if (text.length !in 8..262144) return null
        return withContext(Dispatchers.IO) {
            val revision = ProfileContent.revision(text)
            if (revision == preferences.getString("clipboardNodes", null)) return@withContext null
            preferences.edit().putString("clipboardNodes", revision).apply()
            nodes.detect(text, links = true)
        }
    }

    private suspend fun invokeCapture(method: String, args: Map<String, Any?>): Any? = when (method) {
        "capture.configure" -> changeProfiles { capture.invoke(method, args) }
        "capture.start", "capture.stop" -> operations.withLock { capture.invoke(method, args) }
        else -> capture.invoke(method, args)
    }

    private suspend fun invokeProxy(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "groups.select" -> {
                if (args["profileId"] is Number) {
                    val session = CommandTarget.localSession()
                    changeNodes(session) {
                        nodes.select(args.string("group"), args.string("tag"), args.long("profileId"), args.string("profileRevision")) {
                            CommandTarget.requireLocalSession(session)
                            require(activity.serviceStatus.value == Status.Stopped) {
                                "Wait for the local service to stop before selecting a node"
                            }
                        }
                    }
                } else operations.withLock {
                    val name = args.string("group")
                    val tag = args.string("tag")
                    val group = groups.uiState.value.groups.firstOrNull { it.tag == name } ?: error("Group no longer exists")
                    require(group.selectable) { "This group is selected automatically by sing-box" }
                    require(group.items.any { it.tag == tag }) { "Node no longer exists" }
                    withContext(Dispatchers.IO) { CommandTarget.standaloneClient().selectOutbound(group.tag, tag) }
                }
            }
            "groups.test" -> withContext(Dispatchers.IO) {
                val tag = args.string("tag")
                val known = groups.uiState.value.groups.any { it.tag == tag || it.items.any { item -> item.tag == tag } }
                require(known) { "Node no longer exists" }
                CommandTarget.standaloneClient().urlTest(tag)
            }
            "mode.set" -> {
                val mode = args.string("mode")
                require(mode in dashboard.uiState.value.clashModes) { "This routing mode is not provided by the configuration" }
                withContext(Dispatchers.IO) { CommandTarget.standaloneClient().setClashMode(mode) }
            }
            "systemProxy.set" -> {
                require(!CommandTarget.isRemote && dashboard.uiState.value.systemProxyVisible) { "System HTTP proxy is unavailable" }
                dashboard.toggleSystemProxy(args["enabled"] == true)
            }
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun invokeConnections(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "connections.visible" -> {
                val visible = args["visible"] == true
                if (visible != connectionsVisible.value) {
                    connectionsVisible.value = visible
                    connections.setVisible(visible)
                }
            }
            "connections.close" -> withContext(Dispatchers.IO) { CommandTarget.standaloneClient().closeConnection(args.string("id")) }
            "connections.closeAll" -> withContext(Dispatchers.IO) { CommandTarget.standaloneClient().closeConnections() }
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun invokeLogs(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "logs.clear" -> logs.requestClearLogs()
            "logs.visible" -> logsVisible.value = args["visible"] == true
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun invokeSettings(method: String, args: Map<String, Any?>): Any? = when (method) {
        "preferences.set" -> operations.withLock { savePreferences(args.objectOrNull("value") ?: error("Missing preferences")) }
        "settings.set" -> operations.withLock {
            saveSettings(args)
            settings()
        }
        "settings.patch" -> operations.withLock {
            patchSettings(args.objectOrNull("value") ?: error("Missing settings"))
            settings()
        }
        else -> toolBridge.invoke(method, args)
    }

    private suspend fun savePreferences(values: Map<String, Any?>): Map<String, Any?> {
        val changes = BoxPreferences.validate(values)
        val current = jsonObject(JSONObject(preferences.getString("value", "{}")!!))
        val updated = BoxPreferences.validate(current + changes)
        withContext(Dispatchers.IO) {
            check(preferences.edit().putString("value", JSONObject(updated).toString()).commit()) { "Unable to save preferences" }
        }
        return updated
    }

    private suspend fun invokeApps(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "apps.list" -> return listApps(args)
            "apps.icon" -> return withContext(Dispatchers.IO) { appIcon(args.string("package")) }
            "apps.permission" -> activity.requestShizukuPermission()
            "apps.rescan" -> return operations.withLock {
                if (Settings.perAppProxyManagedMode) {
                    Settings.saveManagedApps(PerAppProxyScanner.scanAllChinaApps())
                    emit("settings", settings())
                }
                settings()
            }
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun listApps(args: Map<String, Any?>): List<Map<String, Any>> {
        val result = withContext(Dispatchers.IO) {
            apps.list(
                key = "${PackageQueryManager.strategy}:${activity.resources.configuration.locales.toLanguageTags()}",
                refresh = args["refresh"] == true,
                showSystem = args["system"] == true,
                showOffline = args["nonInternet"] == true,
            )
        }
        // Package broadcasts may have updated managed selections while the page was closed.
        emit("settings", settings())
        return result
    }

    private fun appIcon(packageName: String): ByteArray {
        val drawable = activity.packageManager.getApplicationIcon(packageName)
        val size = (48 * activity.resources.displayMetrics.density).toInt().coerceIn(48, 192)
        val bitmap = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
        return try {
            drawable.setBounds(0, 0, size, size)
            drawable.draw(Canvas(bitmap))
            ByteArrayOutputStream().use { output ->
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)
                output.toByteArray()
            }
        } finally {
            bitmap.recycle()
        }
    }

    private suspend fun invokeRemote(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "remote.list" -> return withContext(Dispatchers.IO) {
                RemoteServerManager.list().map { mapOf("id" to it.id, "name" to it.name, "url" to it.url, "secret" to it.secret) }
            }
            "remote.save" -> return withContext(Dispatchers.IO) { saveRemoteServer(args) }
            "remote.delete" -> withContext(Dispatchers.IO) {
                val id = args.id()
                require(RemoteControlManager.remoteServer.value?.id != id) { "Disconnect from this server first" }
                RemoteServerManager.get(id)?.let { RemoteServerManager.delete(it) }
            }
            "remote.connect" -> operations.withLock {
                val server = withContext(Dispatchers.IO) { RemoteServerManager.get(args.id()) } ?: error("Server no longer exists")
                capture.cancelBodyReads()
                RemoteControlManager.enterRemoteControl(server)
            }
            "remote.disconnect" -> operations.withLock {
                capture.cancelBodyReads()
                RemoteControlManager.exitRemoteControl()
            }
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun saveRemoteServer(args: Map<String, Any?>): Long {
        val id = (args["id"] as? Number)?.toLong() ?: 0L
        val server = if (id == 0L) RemoteServer() else RemoteServerManager.get(id) ?: error("Server no longer exists")
        server.name = args.string("name")
        server.url = RemoteServer.validateURL(args.string("url")) ?: error("Invalid server address")
        server.secret = args.string("secret")
        if (id == 0L) RemoteServerManager.create(server) else RemoteServerManager.update(server)
        return server.id
    }

    private suspend fun invokeBackup(method: String, args: Map<String, Any?>): Any? {
        val backupScope = args["scope"] as? String ?: "all"
        return when (method) {
            "backup.create" -> operations.withLock { withContext(Dispatchers.IO) { backups.create() } }
            "backup.read" -> {
                val uri = activity.pickDocument() ?: return null
                withContext(Dispatchers.IO) { backups.read(activity.readDocument(uri).second, backupScope) }
            }
            "backup.restore" -> localProfiles {
                val backup = args.objectOrNull("backup") ?: error("Missing backup")
                backups.restore(backup, backupScope, args["strategy"] as? String ?: "Compatible")
            }
            "webdav.get" -> webdav.snapshot()
            "webdav.set" -> operations.withLock {
                withContext(Dispatchers.IO) {
                    require(args.containsKey("value")) { "Missing WebDAV account" }
                    // A null account removes the saved one.
                    val account = if (args["value"] == null) null else args.objectOrNull("value") ?: error("Invalid WebDAV account")
                    webdav.save(account)
                    webdav.snapshot()
                }
            }
            "webdav.check" -> withContext(Dispatchers.IO) { webdav.check() }
            "webdav.upload" -> operations.withLock {
                withContext(Dispatchers.IO) {
                    webdav.upload(backups.encode(backups.create()))
                    true
                }
            }
            "webdav.read" -> withContext(Dispatchers.IO) { backups.read(webdav.download(), backupScope) }
            else -> toolBridge.invoke(method, args)
        }
    }

    private suspend fun <T> localProfiles(session: Long = CommandTarget.localSession(), block: suspend () -> T): T = operations.withLock {
        withContext(Dispatchers.IO) {
            CommandTarget.requireLocalSession(session)
            block()
        }
    }

    private suspend fun <T> changeProfiles(session: Long = CommandTarget.localSession(), block: suspend () -> T): T =
        localProfiles(session) { block().also { publishProfiles() } }

    private suspend fun <T> changeNodes(session: Long = CommandTarget.localSession(), block: suspend () -> T): T = localProfiles(session) {
        var failure: Exception? = null
        try {
            block()
        } catch (error: Exception) {
            failure = error
            throw error
        } finally {
            withContext(NonCancellable) {
                try {
                    publishProfiles(ensureDefault = failure == null)
                } catch (error: Exception) {
                    failure?.addSuppressed(error) ?: throw error
                }
            }
        }
    }

    private suspend fun publishNodes(successful: Boolean) = publishProfiles(ensureDefault = successful)

    // An unreadable document is reported when it is used, not while the interface loads.
    private suspend fun nodeState(): Map<String, Any?> = try {
        nodes.state()
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (_: Exception) {
        mapOf("profileId" to Settings.selectedProfile, "profileRevision" to "", "nodes" to emptyList<Any>(), "groups" to emptyList<Any>())
    }

    private suspend fun publishProfiles(ensureDefault: Boolean = true) {
        val value = withContext(Dispatchers.IO) {
            if (ensureDefault) profiles.list() else ProfileManager.list().map { it.toWire() }
        }
        emit("profiles", value)
        emit("nodes", withContext(Dispatchers.IO) { nodeState() })
        if (::dashboard.isInitialized) emit("dashboard", dashboard.uiState.value.toWire())
        if (::dashboardDetails.isInitialized) dashboardDetails.invalidateNetwork()
    }

    private fun readPreferences(): Map<String, Any?> = runCatching {
        BoxPreferences.validate(jsonObject(JSONObject(preferences.getString("value", "{}")!!)))
    }.getOrDefault(emptyMap())

    private fun batteryExempt(): Boolean =
        activity.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(activity.packageName) == true

    private fun settings(): Map<String, Any?> = mapOf(
        "perAppEnabled" to Settings.perAppProxyEnabled, "perAppMode" to Settings.perAppProxyMode,
        "perAppList" to Settings.perAppProxyList.toList(), "perAppManaged" to Settings.perAppProxyManagedMode,
        "perAppManagedList" to Settings.perAppProxyManagedList.toList(),
        "allowBypass" to Settings.allowBypass, "autoRedirect" to Settings.autoRedirect,
        "dynamicNotification" to Settings.dynamicNotification, "disableDeprecatedWarnings" to Settings.disableDeprecatedWarnings,
        "oomKillerEnabled" to Settings.oomKillerEnabled, "oomKillerDisabled" to Settings.oomKillerDisabled,
        "oomMemoryLimitMB" to Settings.oomMemoryLimitMB, "powerReportEnabled" to Settings.powerReportEnabled,
        "queryMode" to Settings.perAppProxyPackageQueryMode,
        "batteryExempt" to batteryExempt(),
    )

    private suspend fun saveSettings(args: Map<String, Any?>) {
        val key = args["key"] as? String ?: error("Missing setting")
        val values = mapOf(key to args["value"])
        if (args["restart"] != true) {
            patchSettings(values)
            return
        }
        require(key in setOf("allowBypass", "autoRedirect", "dynamicNotification")) { "Invalid automatic service restart setting" }
        require(CommandTarget.isRemote || activity.serviceStatus.value in setOf(Status.Started, Status.Stopped)) {
            "Wait for the service to finish starting or stopping"
        }
        val previous = mapOf(key to settings()[key])
        if (previous == values) return
        if (CommandTarget.isRemote || activity.serviceStatus.value != Status.Started) {
            patchSettings(values)
            return
        }
        ServiceSettingsChange.apply(previous, values, ::patchSettings) { recover ->
            if (!CommandTarget.isRemote) activity.applyProfile(restart = true, recover = recover)
        }
    }

    private suspend fun patchSettings(values: Map<String, Any?>) {
        BoxSettings.validate(values)
        val managed = values["perAppManaged"] as? Boolean ?: Settings.perAppProxyManagedMode
        val enabled = values["perAppEnabled"] as? Boolean ?: Settings.perAppProxyEnabled
        val queryMode = values["queryMode"] as? String ?: Settings.perAppProxyPackageQueryMode
        val managedApps = if (managed && enabled && values.keys.any { it in setOf("perAppManaged", "perAppEnabled", "queryMode") }) {
            PerAppProxyScanner.scanAllChinaApps(queryMode)
        } else null
        // Scan using the requested strategy, then publish all settings together after Room commits.
        Settings.dataStore.putBatchConfirmed(BoxSettings.entities(values, managedApps))
        PackageQueryManager.refreshQueryMode()
        withContext(Dispatchers.IO) {
            Application.application.reloadSetupOptions()
        }
        emit("settings", settings())
    }

    fun reportError(message: String) {
        scope.launch {
            if (sink == null) {
                if (queuedErrors.size >= 10) queuedErrors.removeFirst()
                queuedErrors.addLast(message)
            } else emit("error", message)
        }
    }

    internal fun submitBrowserResult(endpoint: String, challengeID: String, value: io.nekohasekai.sfa.backend.tools.OpenConnectBrowserResultData) {
        scope.launch {
            try {
                require(toolBridge.openConnect.endpoint(endpoint)?.authChallenge?.id == challengeID) { "Authentication request expired" }
                // Keep this response on the session whose challenge was checked.
                val client = CommandTarget.standaloneClient()
                withContext(Dispatchers.IO) {
                    val browserResult = Libbox.newOpenConnectBrowserResult(value.finalURL)
                    value.cookies.forEach { browserResult.addCookie(it.name, it.value) }
                    client.submitOpenConnectAuthResponse(endpoint, challengeID, Libbox.newOpenConnectBrowserAuthResponse(browserResult))
                }
                emit("browserComplete", mapOf("id" to challengeID, "success" to true))
            } catch (e: Exception) { reportError(e.message ?: e.toString()) }
        }
    }

    fun notifyIntent() = emit("intentAvailable", true)

    fun systemAccentColor(): Long = (if (android.os.Build.VERSION.SDK_INT >= 31)
        activity.getColor(android.R.color.system_accent1_500) else 0xff007aff.toInt()).toLong() and 0xffffffffL

    fun refreshSystemAppearance() = emit("systemAccent", systemAccentColor())

    suspend fun prepareForExit() {
        quiescing = true
        scripts.cancelAll()
        capture.cancelBodyReads()
        val pending = calls.toList()
        pending.forEach { it.cancel() }
        pending.joinAll()
        initialize.join()
    }

    fun resume() { quiescing = false }

    suspend fun browserRequest(endpoint: String, challengeID: String): Map<String, Any?> {
        initialize.await()
        val challenge = toolBridge.openConnect.endpoint(endpoint)?.authChallenge
        require(challenge?.id == challengeID) { "Authentication request expired" }
        return challenge?.browser?.toWire() ?: error("Browser request no longer exists")
    }

    internal fun emit(type: String, value: Any?) {
        scope.launch(Dispatchers.Main.immediate) { sink?.success(mapOf("type" to type, "value" to value)) }
    }

    override fun close() {
        fun cleanup(work: () -> Unit) {
            runCatching(work).onFailure { Log.w("BoxBridge", "Native bridge cleanup", it) }
        }
        cleanup(warp::close)
        cleanup(scripts::close)
        cleanup(capture::close)
        cleanup(qrScanner::close)
        cleanup { channel.setMethodCallHandler(null) }
        cleanup { events.setStreamHandler(null) }
        streamJob?.cancel()
        sink = null
        if (::dashboard.isInitialized) cleanup { dashboard.commandClient.removeHandler(rates) }
        if (profileCallbackRegistered) cleanup { ProfileManager.unregisterCallback(profileCallback) }
        if (packageListenersRegistered) cleanup { PackageQueryManager.unregisterListeners() }
        if (::connections.isInitialized && connectionsVisible.value) cleanup { connections.setVisible(false) }
        if (::toolBridge.isInitialized) cleanup(toolBridge::close)
        if (::dashboardDetails.isInitialized) cleanup(dashboardDetails::close)
    }
}

internal fun jsonObject(value: JSONObject): Map<String, Any?> = value.keys().asSequence().associateWith { jsonValue(value.get(it)) }
private fun jsonValue(value: Any?): Any? = when (value) {
    JSONObject.NULL -> null
    is JSONObject -> jsonObject(value)
    is JSONArray -> (0 until value.length()).map { jsonValue(value.get(it)) }
    else -> value
}

private fun Map<String, Any?>.string(key: String): String = this[key] as? String ?: error("Missing argument: $key")
private fun Map<String, Any?>.long(key: String): Long = (this[key] as? Number)?.toLong() ?: error("Missing argument: $key")
private fun Map<String, Any?>.id(): Long = long("id")

@Suppress("UNCHECKED_CAST")
private fun Map<String, Any?>.objectOrNull(key: String): Map<String, Any?>? = this[key] as? Map<String, Any?>
