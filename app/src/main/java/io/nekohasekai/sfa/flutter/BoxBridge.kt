package io.nekohasekai.sfa.flutter

import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.net.Uri
import android.os.PowerManager
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
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
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.ProfileContent
import io.nekohasekai.sfa.database.RemoteServer
import io.nekohasekai.sfa.database.RemoteServerManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.CommandTarget
import io.nekohasekai.sfa.utils.RemoteControlManager
import io.nekohasekai.sfa.vendor.PackageQueryManager
import io.nekohasekai.sfa.vendor.PerAppProxyScanner
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.Closeable

class BoxBridge(private val activity: BoxActivity, messenger: BinaryMessenger) : Closeable {
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
    private val capture = BoxCapture(activity)
    private val qrScanner = BoxQrScanner(profiles::decode)
    private val preferences = activity.getSharedPreferences("flclash_ui", 0)
    private val webdav = BoxWebDAV(activity)
    private val backups = BoxBackups(activity, ::settings, ::patchSettings,
        { restart, recover -> withContext(Dispatchers.Main) { activity.applyProfile(restart, recover) } }, ::publishProfiles, ::emit, webdav)
    private val rates = object : CommandClient.Handler {
        override fun updateStatus(status: StatusMessage) {
            if (::dashboardDetails.isInitialized) dashboardDetails.remoteMemory = status.memory
            emit("rates", mapOf("upload" to status.uplink, "download" to status.downlink, "memoryBytes" to status.memory))
        }
    }
    private val profileCallback: () -> Unit = { scope.launch { operations.withLock { publishProfiles() } } }
    private val initialize = scope.async {
        withContext(Dispatchers.IO) {
            Settings.dataStore.initialize()
            Application.application.setupLibbox()
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
        PackageQueryManager.registerListeners()
        RemoteControlManager.restore()
        scope.launch {
            activity.serviceStatus.collect {
                dashboard.updateServiceStatus(it)
                groups.updateServiceStatus(it)
                connections.updateServiceStatus(it)
                logs.updateServiceStatus(it)
                if (it == Status.Stopped) emit("rates", mapOf("upload" to 0L, "download" to 0L))
            }
        }
        scope.launch {
            GlobalEventBus.events.collect { event ->
                when (event) {
                    is UiEvent.ErrorMessage -> reportError(event.message)
                }
            }
        }
    }

    init {
        channel.setMethodCallHandler { call, result ->
            scope.launch {
                try {
                    initialize.await()
                    result.success(invoke(call))
                } catch (e: CancellationException) {
                    result.error("cancelled", "Operation cancelled", null)
                } catch (e: Exception) {
                    result.error("box_error", e.message ?: e.toString(), null)
                }
            }
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
            "capture" -> invokeCapture(method, args)
            "groups", "mode", "systemProxy" -> invokeProxy(method, args)
            "connections" -> invokeConnections(method, args)
            "logs" -> invokeLogs(method, args)
            "preferences", "settings" -> invokeSettings(method, args)
            "apps" -> invokeApps(method, args)
            "remote" -> invokeRemote(method, args)
            "backup", "webdav" -> invokeBackup(method, args)
            "reports" -> reports.invoke(method, args)
            else -> invokeGeneral(method, args)
        }
    }

    private suspend fun invokeGeneral(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "ready" -> return ready()
            "updates.check" -> return withContext(Dispatchers.IO) { BoxUpdates.check() }
            "intent.take" -> return activity.takeIntent()
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
                activity.startActivity(Intent.createChooser(send, null))
            }
            "battery.open" -> {
                val action = android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS
                activity.startActivity(Intent(action, Uri.parse("package:" + activity.packageName)))
            }
            "url.open" -> {
                val uri = Uri.parse(args.string("url"))
                require(uri.scheme in listOf("http", "https")) { "Unsupported URL" }
                activity.startActivity(Intent(Intent.ACTION_VIEW, uri))
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
        // The dashboard and the pending intent are read only after the stored state has loaded.
        val profileList = initialProfiles.await()
        val settingValues = initialSettings.await()
        val preferenceValues = initialPreferences.await()
        mapOf(
            "version" to BuildConfig.VERSION_NAME,
            "coreVersion" to Libbox.version(),
            "systemAccentColor" to systemAccentColor(),
            "dashboard" to dashboard.uiState.value.toWire(),
            "profiles" to profileList,
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
            "profiles.importResource" -> return importResource(args)
            "profiles.scan" -> return withContext(Dispatchers.Default) { qrScanner.scan(args.string("session"), args.string("text")) }
            "profiles.scan.close" -> qrScanner.close(args.string("session"))
            "profiles.decodeIntent" -> return decodeIntent(args.string("uri"))
            else -> return toolBridge.invoke(method, args)
        }
        return null
    }

    private suspend fun saveProfile(args: Map<String, Any?>): Long {
        val session = CommandTarget.localSession()
        // URL imports download before entering the operation lock.
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

    private fun decodeDocument(uri: Uri): Map<String, Any?> {
        val (name, bytes) = activity.readDocument(uri)
        return profiles.decode(bytes, name)
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

    private suspend fun invokeCapture(method: String, args: Map<String, Any?>): Any? = when (method) {
        "capture.configure" -> changeProfiles { capture.invoke(method, args) }
        "capture.start", "capture.stop" -> operations.withLock { capture.invoke(method, args) }
        else -> capture.invoke(method, args)
    }

    private suspend fun invokeProxy(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "groups.select" -> operations.withLock {
                val name = args.string("group")
                val tag = args.string("tag")
                val group = groups.uiState.value.groups.firstOrNull { it.tag == name } ?: error("Group no longer exists")
                require(group.selectable) { "This group is selected automatically by sing-box" }
                require(group.items.any { it.tag == tag }) { "Node no longer exists" }
                withContext(Dispatchers.IO) { CommandTarget.standaloneClient().selectOutbound(group.tag, tag) }
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
            "apps.permission" -> PackageQueryManager.requestShizukuPermission()
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
                RemoteControlManager.enterRemoteControl(server)
            }
            "remote.disconnect" -> operations.withLock { RemoteControlManager.exitRemoteControl() }
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

    private suspend fun publishProfiles() {
        val value = withContext(Dispatchers.IO) { profiles.list() }
        emit("profiles", value)
        if (::dashboard.isInitialized) emit("dashboard", dashboard.uiState.value.toWire())
        if (::dashboardDetails.isInitialized) dashboardDetails.invalidateNetwork()
    }

    private fun readPreferences(): Map<String, Any?> = runCatching {
        BoxPreferences.validate(jsonObject(JSONObject(preferences.getString("value", "{}")!!)))
    }.getOrDefault(emptyMap())

    private fun settings(): Map<String, Any?> = mapOf(
        "perAppEnabled" to Settings.perAppProxyEnabled, "perAppMode" to Settings.perAppProxyMode,
        "perAppList" to Settings.perAppProxyList.toList(), "perAppManaged" to Settings.perAppProxyManagedMode,
        "perAppManagedList" to Settings.perAppProxyManagedList.toList(),
        "allowBypass" to Settings.allowBypass, "autoRedirect" to Settings.autoRedirect,
        "dynamicNotification" to Settings.dynamicNotification, "disableDeprecatedWarnings" to Settings.disableDeprecatedWarnings,
        "oomKillerEnabled" to Settings.oomKillerEnabled, "oomKillerDisabled" to Settings.oomKillerDisabled,
        "oomMemoryLimitMB" to Settings.oomMemoryLimitMB, "powerReportEnabled" to Settings.powerReportEnabled,
        "queryMode" to Settings.perAppProxyPackageQueryMode,
        "batteryExempt" to (activity.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(activity.packageName) == true),
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

    internal fun emit(type: String, value: Any?) {
        scope.launch(Dispatchers.Main.immediate) { sink?.success(mapOf("type" to type, "value" to value)) }
    }

    override fun close() {
        qrScanner.close()
        channel.setMethodCallHandler(null)
        events.setStreamHandler(null)
        streamJob?.cancel()
        sink = null
        if (::dashboard.isInitialized) {
            dashboard.commandClient.removeHandler(rates)
            ProfileManager.unregisterCallback(profileCallback)
            PackageQueryManager.unregisterListeners()
            if (connectionsVisible.value) connections.setVisible(false)
            toolBridge.close()
            dashboardDetails.close()
        }
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
