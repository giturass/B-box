package io.nekohasekai.sfa.flutter

import android.content.Intent
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
        fun string(key: String): String = args[key] as? String ?: error("Missing argument: " + key)
        fun id(key: String = "id"): Long = (args[key] as? Number)?.toLong() ?: error("Missing argument: " + key)
        return when (call.method) {
            "updates.check" -> withContext(Dispatchers.IO) { BoxUpdates.check() }
            "dashboard.egress" -> dashboardDetails.egress(string("id"))
            "dashboard.probe.open" -> { dashboardDetails.openProbe(string("id")); null }
            "dashboard.probe.get" -> dashboardDetails.query(string("id"), string("url"), id("timeoutMillis"))
            "dashboard.probe.close" -> { dashboardDetails.closeProbe(string("id")); null }
            "dashboard.qualityCache.get" -> dashboardDetails.readQualityCache(string("ip"))
            "dashboard.qualityCache.put" -> {
                @Suppress("UNCHECKED_CAST")
                val value = args["value"] as? Map<String, Any?> ?: error("Missing IP quality record")
                dashboardDetails.writeQualityCache(string("ip"), value)
                null
            }
            "dashboard.releaseMemory" -> dashboardDetails.releaseMemory()
            "dashboard.memory.visible" -> { dashboardDetails.setMemoryVisible(args["visible"] == true); null }
            "dashboard.lan.visible" -> { dashboardDetails.setLanVisible(args["visible"] == true); null }
            "dashboard.lan.refresh" -> dashboardDetails.refreshNetwork(force = true)
            "dashboard.lan.set" -> localProfiles {
                val enabled = args["enabled"] as? Boolean ?: error("Missing LAN proxy state")
                val port = args["port"]?.let {
                    val number = it as? Number ?: error("Invalid proxy port")
                    require(number.toDouble() == number.toInt().toDouble() && number.toInt() in 1..65535) { "Invalid proxy port" }
                    number.toInt()
                } ?: 0
                profiles.setLanProxy(id("profileId"), string("revision"), enabled, port)
                publishProfiles()
                dashboardDetails.refreshNetwork()
            }
            "ready" -> coroutineScope {
                val initialProfiles = async(Dispatchers.IO) { profiles.list() }
                val initialSettings = async(Dispatchers.IO) { settings() }
                val initialPreferences = async(Dispatchers.IO) { readPreferences() }
                val profileList = initialProfiles.await()
                val settingValues = initialSettings.await()
                val preferenceValues = initialPreferences.await()
                mapOf(
                    "version" to BuildConfig.VERSION_NAME, "coreVersion" to Libbox.version(),
                    "systemAccentColor" to systemAccentColor(),
                    "dashboard" to dashboard.uiState.value.toWire(),
                    "profiles" to profileList, "settings" to settingValues, "preferences" to preferenceValues,
                    "intent" to activity.takeIntent(),
                )
            }
            "service.start" -> {
                require(!CommandTarget.isRemote) { "Exit remote control before starting the local service" }
                activity.startService()
                null
            }
            "service.stop" -> {
                require(!CommandTarget.isRemote) { "Exit remote control before stopping the local service" }
                activity.stopService()
                null
            }
            "service.reload" -> operations.withLock {
                if (CommandTarget.isRemote) withContext(Dispatchers.IO) { CommandTarget.standaloneClient().serviceReload() }
                else activity.applyProfile(args["restart"] == true)
                null
            }
            "profiles.read" -> withContext(Dispatchers.IO) { profiles.read(id()) }
            "profiles.config" -> withContext(Dispatchers.IO) { profiles.config(id()) }
            "profiles.stats" -> withContext(Dispatchers.IO) { profiles.stats(id()) }
            "profiles.write" -> localProfiles {
                val saved = profiles.writeContent(id(), string("content"), string("revision"))
                publishProfiles()
                saved
            }
            "capture.configure" -> localProfiles {
                val saved = capture.invoke(call.method, args)
                publishProfiles()
                saved
            }
            "capture.start", "capture.stop" -> operations.withLock { capture.invoke(call.method, args) }
            "profiles.select" -> localProfiles {
                profiles.select(id()) { activity.serviceStatus.value }
                publishProfiles()
                null
            }
            "profiles.save" -> {
                val session = CommandTarget.localSession()
                val prepared = withContext(Dispatchers.IO) { profiles.prepareSave(args) }
                localProfiles(session) {
                    val saved = profiles.savePrepared(prepared) { CommandTarget.requireLocalSession(session) }
                    publishProfiles()
                    saved
                }
            }
            "profiles.update" -> {
                val session = CommandTarget.localSession()
                val snapshot = localProfiles(session) { profiles.prepareUpdate(id()) }
                val content = withContext(Dispatchers.IO) { profiles.downloadUpdate(snapshot) }
                localProfiles(session) {
                    profiles.commitUpdate(snapshot, content) { CommandTarget.requireLocalSession(session) }
                    publishProfiles()
                    null
                }
            }
            "profiles.delete" -> localProfiles {
                profiles.delete(id())
                publishProfiles()
                null
            }
            "profiles.reorder" -> localProfiles {
                profiles.reorder((args["ids"] as List<*>).map { (it as Number).toLong() })
                publishProfiles()
                null
            }
            "profiles.importFile" -> {
                val uri = activity.pickDocument() ?: return null
                withContext(Dispatchers.IO) {
                    val (name, bytes) = activity.readDocument(uri)
                    profiles.decode(bytes, name)
                }
            }
            "profiles.importResource" -> {
                val session = CommandTarget.localSession()
                val profileId = id()
                val section = string("section")
                val revision = string("revision")
                val uri = activity.pickDocument() ?: return null
                var prepared: BoxProfiles.PreparedResource? = null
                try {
                    withContext(Dispatchers.IO) {
                        CommandTarget.requireLocalSession(session)
                        val name = activity.documentName(uri, "resource")
                        activity.contentResolver.openInputStream(uri)?.use { input ->
                            prepared = profiles.prepareResource(profileId, section, revision, name, input)
                        } ?: error("Unable to read resource document")
                    }
                    localProfiles(session) {
                        val saved = profiles.commitResource(checkNotNull(prepared)) { CommandTarget.requireLocalSession(session) }
                        publishProfiles()
                        saved
                    }
                } finally {
                    withContext(NonCancellable + Dispatchers.IO) { prepared?.close() }
                }
            }
            "profiles.scan" -> withContext(Dispatchers.Default) { qrScanner.scan(string("session"), string("text")) }
            "profiles.scan.close" -> { qrScanner.close(string("session")); null }
            "profiles.decodeIntent" -> {
                val value = string("uri")
                val uri = Uri.parse(value)
                if (uri.scheme == "sing-box" && uri.host == "import-remote-profile") {
                    val info = Libbox.parseRemoteProfileImportLink(value)
                    mapOf("name" to info.name, "url" to info.url)
                } else if (uri.scheme == "http" || uri.scheme == "https") {
                    BoxProfiles.validateUrl(value)
                    mapOf("name" to (uri.host ?: "Profile"), "url" to value)
                } else {
                    withContext(Dispatchers.IO) {
                        val (name, bytes) = activity.readDocument(uri)
                        profiles.decode(bytes, name)
                    }
                }
            }
            "intent.take" -> activity.takeIntent()
            "config.check" -> withContext(Dispatchers.IO) { BoxProfiles.validate(string("content")); true }
            "config.format" -> withContext(Dispatchers.IO) { Libbox.formatConfig(string("content")).value }
            "document.save" -> {
                val uri = activity.pickDocument(string("name")) ?: return false
                withContext(Dispatchers.IO) {
                    activity.contentResolver.openOutputStream(uri, "wt")?.use { it.write(string("content").toByteArray()) }
                        ?: error("Unable to write document")
                }
                true
            }
            "share.text" -> {
                activity.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).apply {
                    type = "text/plain"
                    putExtra(Intent.EXTRA_TEXT, string("content"))
                }, null))
                null
            }
            "groups.select" -> operations.withLock {
                val group = groups.uiState.value.groups.firstOrNull { it.tag == string("group") } ?: error("Group no longer exists")
                require(group.selectable) { "This group is selected automatically by sing-box" }
                require(group.items.any { it.tag == string("tag") }) { "Node no longer exists" }
                withContext(Dispatchers.IO) { CommandTarget.standaloneClient().selectOutbound(group.tag, string("tag")) }
                null
            }
            "groups.test" -> withContext(Dispatchers.IO) {
                val tag = string("tag")
                require(groups.uiState.value.groups.any { it.tag == tag || it.items.any { item -> item.tag == tag } }) { "Node no longer exists" }
                CommandTarget.standaloneClient().urlTest(tag)
                null
            }
            "mode.set" -> {
                require(dashboard.uiState.value.clashModes.contains(string("mode"))) { "This routing mode is not provided by the configuration" }
                withContext(Dispatchers.IO) { CommandTarget.standaloneClient().setClashMode(string("mode")) }
                null
            }
            "systemProxy.set" -> {
                require(!CommandTarget.isRemote && dashboard.uiState.value.systemProxyVisible) { "System HTTP proxy is unavailable" }
                val enabled = args["enabled"] == true
                dashboard.toggleSystemProxy(enabled)
                null
            }
            "connections.visible" -> {
                val visible = args["visible"] == true
                if (visible != connectionsVisible.value) {
                    connectionsVisible.value = visible
                    connections.setVisible(visible)
                }
                null
            }
            "connections.close" -> withContext(Dispatchers.IO) {
                CommandTarget.standaloneClient().closeConnection(string("id"))
                null
            }
            "connections.closeAll" -> withContext(Dispatchers.IO) { CommandTarget.standaloneClient().closeConnections(); null }
            "logs.clear" -> { logs.requestClearLogs(); null }
            "logs.visible" -> { logsVisible.value = args["visible"] == true; null }
            "preferences.set" -> operations.withLock {
                @Suppress("UNCHECKED_CAST")
                val value = BoxPreferences.validate(args["value"] as? Map<String, Any?> ?: error("Missing preferences"))
                val old = jsonObject(JSONObject(preferences.getString("value", "{}")!!))
                val updated = BoxPreferences.validate(old + value)
                withContext(Dispatchers.IO) { check(preferences.edit().putString("value", JSONObject(updated).toString()).commit()) { "Unable to save preferences" } }
                updated
            }
            "settings.set" -> operations.withLock { saveSettings(args); settings() }
            "settings.patch" -> operations.withLock {
                @Suppress("UNCHECKED_CAST")
                val values = args["value"] as? Map<String, Any?> ?: error("Missing settings")
                patchSettings(values)
                settings()
            }
            "apps.list" -> {
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
                result
            }
            "apps.icon" -> withContext(Dispatchers.IO) {
                val drawable = activity.packageManager.getApplicationIcon(string("package"))
                val size = (48 * activity.resources.displayMetrics.density).toInt().coerceIn(48, 192)
                val bitmap = android.graphics.Bitmap.createBitmap(size, size, android.graphics.Bitmap.Config.ARGB_8888)
                try {
                    drawable.setBounds(0, 0, size, size)
                    drawable.draw(android.graphics.Canvas(bitmap))
                    java.io.ByteArrayOutputStream().use { output ->
                        bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, output)
                        output.toByteArray()
                    }
                } finally { bitmap.recycle() }
            }
            "apps.permission" -> { PackageQueryManager.requestShizukuPermission(); null }
            "apps.rescan" -> operations.withLock {
                if (Settings.perAppProxyManagedMode) {
                    Settings.saveManagedApps(PerAppProxyScanner.scanAllChinaApps())
                    emit("settings", settings())
                }
                settings()
            }
            "battery.open" -> {
                activity.startActivity(Intent(android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:" + activity.packageName)))
                null
            }
            "url.open" -> {
                val uri = Uri.parse(string("url"))
                require(uri.scheme in listOf("http", "https")) { "Unsupported URL" }
                activity.startActivity(Intent(Intent.ACTION_VIEW, uri))
                null
            }
            "remote.list" -> withContext(Dispatchers.IO) { RemoteServerManager.list().map { mapOf("id" to it.id, "name" to it.name, "url" to it.url, "secret" to it.secret) } }
            "remote.save" -> withContext(Dispatchers.IO) {
                val serverId = (args["id"] as? Number)?.toLong() ?: 0L
                val server = if (serverId == 0L) RemoteServer() else RemoteServerManager.get(serverId) ?: error("Server no longer exists")
                server.name = string("name")
                server.url = RemoteServer.validateURL(string("url")) ?: error("Invalid server address")
                server.secret = string("secret")
                if (serverId == 0L) RemoteServerManager.create(server) else RemoteServerManager.update(server)
                server.id
            }
            "remote.delete" -> withContext(Dispatchers.IO) {
                require(RemoteControlManager.remoteServer.value?.id != id()) { "Disconnect from this server first" }
                RemoteServerManager.get(id())?.let { RemoteServerManager.delete(it) }
                null
            }
            "remote.connect" -> operations.withLock {
                val server = withContext(Dispatchers.IO) { RemoteServerManager.get(id()) } ?: error("Server no longer exists")
                RemoteControlManager.enterRemoteControl(server)
                null
            }
            "remote.disconnect" -> operations.withLock { RemoteControlManager.exitRemoteControl(); null }
            "backup.create" -> operations.withLock { withContext(Dispatchers.IO) { backups.create() } }
            "backup.read" -> {
                val uri = activity.pickDocument() ?: return null
                withContext(Dispatchers.IO) {
                    backups.read(activity.readDocument(uri).second, args["scope"] as? String ?: "all")
                }
            }
            "backup.restore" -> localProfiles {
                @Suppress("UNCHECKED_CAST")
                val backup = args["backup"] as? Map<String, Any?> ?: error("Missing backup")
                backups.restore(backup, args["scope"] as? String ?: "all", args["strategy"] as? String ?: "Compatible")
            }
            "webdav.get" -> webdav.snapshot()
            "webdav.set" -> operations.withLock { withContext(Dispatchers.IO) {
                require(args.containsKey("value")) { "Missing WebDAV account" }
                @Suppress("UNCHECKED_CAST")
                val value = args["value"]?.let { it as? Map<String, Any?> ?: error("Invalid WebDAV account") }
                webdav.save(value)
                webdav.snapshot()
            } }
            "webdav.check" -> withContext(Dispatchers.IO) { webdav.check() }
            "webdav.upload" -> operations.withLock { withContext(Dispatchers.IO) {
                webdav.upload(backups.encode(backups.create()))
                true
            } }
            "webdav.read" -> withContext(Dispatchers.IO) { backups.read(webdav.download(), args["scope"] as? String ?: "all") }
            "terminal.set", "terminal.importFont", "terminal.deleteFont" -> operations.withLock { toolBridge.invoke(call.method, args) }
            else -> when {
                call.method.startsWith("capture.") -> capture.invoke(call.method, args)
                call.method.startsWith("reports.") -> reports.invoke(call.method, args)
                else -> toolBridge.invoke(call.method, args)
            }
        }
    }

    private suspend fun <T> localProfiles(session: Long = CommandTarget.localSession(), block: suspend () -> T): T {
        return operations.withLock { withContext(Dispatchers.IO) {
            CommandTarget.requireLocalSession(session)
            block()
        } }
    }

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
