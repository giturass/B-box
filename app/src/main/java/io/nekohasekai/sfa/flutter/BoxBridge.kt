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
import io.nekohasekai.sfa.compose.base.GlobalEventBus
import io.nekohasekai.sfa.compose.base.UiEvent
import io.nekohasekai.sfa.compose.screen.connections.ConnectionsViewModel
import io.nekohasekai.sfa.compose.screen.dashboard.DashboardViewModel
import io.nekohasekai.sfa.compose.screen.dashboard.groups.GroupsViewModel
import io.nekohasekai.sfa.compose.screen.log.LogViewModel
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.RemoteServer
import io.nekohasekai.sfa.database.RemoteServerManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandClient
import io.nekohasekai.sfa.utils.CommandTarget
import io.nekohasekai.sfa.utils.RemoteControlManager
import io.nekohasekai.sfa.vendor.PackageQueryManager
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.Closeable
import java.io.File

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
    private var connectionsVisible = false
    private val operations = Mutex()
    private val profiles = BoxProfiles { withContext(Dispatchers.Main) { activity.applyProfile() } }
    private val preferences = activity.getSharedPreferences("flclash_ui", 0)
    private val rates = object : CommandClient.Handler {
        override fun updateStatus(status: StatusMessage) {
            emit("rates", mapOf("upload" to status.uplink, "download" to status.downlink, "memoryBytes" to status.memory))
        }
    }
    private val profileCallback: () -> Unit = { scope.launch { publishProfiles() } }
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
                    is UiEvent.RequestStartService -> activity.startService()
                    is UiEvent.ApplyServiceChange -> emit("applyRequired", event.mode.name)
                    is UiEvent.OpenUrl -> emit("openUrl", event.url)
                    else -> Unit
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
                        while (queuedErrors.isNotEmpty()) emit("error", queuedErrors.removeFirst())
                        coroutineScope {
                            launch { dashboard.uiState.collect { emit("dashboard", it.toWire()) } }
                            launch { groups.uiState.collect { emit("groups", it.groups.map { group -> group.toWire() }) } }
                            launch { connections.uiState.collect { state ->
                                if (connectionsVisible) emit("connections", withContext(Dispatchers.Default) { state.allConnections.map { it.toWire() } })
                            } }
                            launch { logs.uiState.collect { state ->
                                emit("logs", withContext(Dispatchers.Default) { state.logs.map { mapOf("id" to it.id, "level" to it.entry.level.label, "message" to it.annotatedString.text) } })
                            } }
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
                if (::connections.isInitialized && connectionsVisible) {
                    connectionsVisible = false
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
            "ready" -> mapOf(
                "version" to BuildConfig.VERSION_NAME, "coreVersion" to Libbox.version(),
                "dashboard" to dashboard.uiState.value.toWire(),
                "profiles" to withContext(Dispatchers.IO) { profiles.list() },
                "settings" to settings(), "preferences" to readPreferences(),
                "tools" to toolBridge.snapshot(), "diagnostics" to toolBridge.diagnostics(),
                "intent" to activity.takeIntent(),
            )
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
            "profiles.list" -> withContext(Dispatchers.IO) { profiles.list() }
            "profiles.read" -> withContext(Dispatchers.IO) { profiles.read(id()) }
            "profiles.select" -> localProfiles {
                profiles.select(id())
                publishProfiles()
                null
            }
            "profiles.save" -> localProfiles {
                val saved = profiles.save(args)
                publishProfiles()
                saved
            }
            "profiles.update" -> localProfiles {
                profiles.update(id())
                publishProfiles()
                null
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
                if (visible != connectionsVisible) {
                    connectionsVisible = visible
                    connections.setVisible(visible)
                }
                if (visible) emit("connections", connections.uiState.value.allConnections.map { it.toWire() })
                null
            }
            "connections.close" -> withContext(Dispatchers.IO) {
                CommandTarget.standaloneClient().closeConnection(string("id"))
                null
            }
            "connections.closeAll" -> withContext(Dispatchers.IO) { CommandTarget.standaloneClient().closeConnections(); null }
            "logs.clear" -> { logs.requestClearLogs(); null }
            "preferences.set" -> {
                @Suppress("UNCHECKED_CAST")
                val value = args["value"] as? Map<String, Any?> ?: error("Missing preferences")
                BoxPreferences.validate(value)
                val old = JSONObject(preferences.getString("value", "{}")!!)
                value.forEach { (key, item) -> old.put(key.toString(), JSONObject.wrap(item)) }
                withContext(Dispatchers.IO) { check(preferences.edit().putString("value", old.toString()).commit()) { "Unable to save preferences" } }
                readPreferences()
            }
            "settings.get" -> settings()
            "settings.set" -> { saveSettings(args); settings() }
            "apps.list" -> withContext(Dispatchers.IO) {
                PackageQueryManager.getInstalledPackages(0).map { info ->
                    val application = info.applicationInfo
                    mapOf(
                        "package" to info.packageName,
                        "name" to (application?.loadLabel(activity.packageManager)?.toString() ?: info.packageName),
                        "system" to (application != null && application.flags and android.content.pm.ApplicationInfo.FLAG_SYSTEM != 0),
                    )
                }.sortedBy { (it["name"] as String).lowercase() }
            }
            "apps.permission" -> { PackageQueryManager.requestShizukuPermission(); null }
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
            "remote.connect" -> {
                val server = withContext(Dispatchers.IO) { RemoteServerManager.get(id()) } ?: error("Server no longer exists")
                RemoteControlManager.enterRemoteControl(server)
                null
            }
            "remote.disconnect" -> { RemoteControlManager.exitRemoteControl(); null }
            "backup.create" -> withContext(Dispatchers.IO) {
                mapOf("format" to "bbox-1", "selectedProfile" to Settings.selectedProfile,
                    "profiles" to ProfileManager.list().map { it.toWire() + ("content" to File(it.typed.path).readText()) },
                    "preferences" to readPreferences(),
                    "settings" to settings().filterKeys { it !in setOf("batteryExempt", "perAppManagedList") },
                    "servers" to RemoteServerManager.list().map { mapOf("name" to it.name, "url" to it.url, "secret" to it.secret) })
            }
            "backup.read" -> {
                val uri = activity.pickDocument() ?: return null
                withContext(Dispatchers.IO) {
                    val (_, data) = activity.readDocument(uri)
                    val backup = jsonObject(JSONObject(data.toString(Charsets.UTF_8)))
                    validateBackup(backup)
                    backup
                }
            }
            "backup.restore" -> localProfiles {
                @Suppress("UNCHECKED_CAST")
                val backup = args["backup"] as? Map<String, Any?> ?: error("Missing backup")
                val entries = validateBackup(backup)
                val oldSelection = Settings.selectedProfile
                val created = mutableListOf<Long>()
                val servers = mutableListOf<RemoteServer>()
                val oldSettings = settings().filterKeys { it !in setOf("batteryExempt", "perAppManagedList") }
                val oldPreferences = preferences.getString("value", "{}")!!
                try {
                    val mapping = mutableMapOf<Long, Long>()
                    for (entry in entries) {
                        val originalID = (entry["id"] as? Number)?.toLong() ?: 0L
                        val newID = profiles.save(entry - "id")
                        created.add(newID)
                        mapping[originalID] = newID
                    }
                    check(preferences.edit().putString("value", JSONObject(backup["preferences"] as? Map<*, *> ?: emptyMap<String, Any>()).toString()).commit())
                    for (entry in rowsOf(backup["servers"])) {
                        val server = RemoteServer().apply {
                            name = entry["name"] as String
                            url = entry["url"] as String
                            secret = entry["secret"] as? String ?: ""
                        }
                        RemoteServerManager.create(server)
                        servers.add(server)
                    }
                    @Suppress("UNCHECKED_CAST")
                    val restoredSettings = backup["settings"] as? Map<String, Any?> ?: emptyMap()
                    for ((key, value) in restoredSettings) saveSettings(mapOf("key" to key, "value" to value))
                    val selection = mapping[(backup["selectedProfile"] as? Number)?.toLong()]
                    if (selection != null) profiles.select(selection)
                    publishProfiles()
                    emit("preferences", readPreferences())
                    created.size
                } catch (e: Exception) {
                    Settings.selectedProfile = oldSelection
                    created.forEach { runCatching { profiles.delete(it) } }
                    servers.forEach { runCatching { RemoteServerManager.delete(it) } }
                    oldSettings.forEach { (key, value) -> runCatching { saveSettings(mapOf("key" to key, "value" to value)) } }
                    preferences.edit().putString("value", oldPreferences).commit()
                    throw e
                }
            }
            else -> toolBridge.invoke(call.method, args)
        }
    }

    private suspend fun <T> localProfiles(block: suspend () -> T): T {
        require(!CommandTarget.isRemote) { "Local profiles cannot modify a remote sing-box session" }
        return operations.withLock { withContext(Dispatchers.IO) { block() } }
    }

    private suspend fun publishProfiles() {
        val value = withContext(Dispatchers.IO) { profiles.list() }
        emit("profiles", value)
        if (::dashboard.isInitialized) emit("dashboard", dashboard.uiState.value.toWire())
    }

    private fun readPreferences(): Map<String, Any?> = runCatching {
        BoxPreferences.validate(jsonObject(JSONObject(preferences.getString("value", "{}")!!)))
    }.getOrDefault(emptyMap())

    private fun validateBackup(backup: Map<String, Any?>): List<Map<String, Any?>> {
        require(backup["format"] == "bbox-1") { "Unsupported B-box backup format" }
        @Suppress("UNCHECKED_CAST")
        val entries = backup["profiles"] as? List<Map<String, Any?>> ?: error("Backup has no profiles")
        require(entries.size <= 1000) { "Backup contains too many profiles" }
        entries.forEach {
            require((it["name"] as? String)?.isNotBlank() == true) { "Profile name is required" }
            BoxProfiles.validate(it["content"] as? String ?: error("Missing profile content"))
            val url = it["url"] as? String ?: ""
            if (url.isNotEmpty()) BoxProfiles.validateUrl(url)
            require((it["interval"] as? Number)?.toInt()?.let { interval -> interval >= 15 } != false) { "Invalid update interval" }
        }
        @Suppress("UNCHECKED_CAST")
        BoxPreferences.validate(backup["preferences"] as? Map<String, Any?> ?: emptyMap())
        @Suppress("UNCHECKED_CAST")
        BoxSettings.validate(backup["settings"] as? Map<String, Any?> ?: emptyMap())
        val servers = rowsOf(backup["servers"])
        require(servers.size <= 1000) { "Too many remote servers" }
        servers.forEach {
            require(it["name"] is String && (it["url"] as? String)?.let(RemoteServer::validateURL) != null) { "Invalid remote server" }
            require(it["secret"] == null || it["secret"] is String) { "Invalid remote secret" }
        }
        return entries
    }

    @Suppress("UNCHECKED_CAST")
    private fun rowsOf(value: Any?): List<Map<String, Any?>> = if (value == null) emptyList() else
        (value as? List<*>)?.map { it as? Map<String, Any?> ?: error("Invalid backup entry") } ?: error("Invalid backup list")

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
        BoxSettings.validate(mapOf((args["key"] as? String ?: error("Missing setting")) to args["value"]))
        when (val key = args["key"] as? String) {
            "perAppEnabled" -> Settings.perAppProxyEnabled = args["value"] == true
            "perAppMode" -> {
                val value = (args["value"] as Number).toInt()
                require(value == Settings.PER_APP_PROXY_INCLUDE || value == Settings.PER_APP_PROXY_EXCLUDE)
                Settings.perAppProxyMode = value
            }
            "perAppList" -> Settings.perAppProxyList = (args["value"] as List<*>).map { it as String }.toSet()
            "perAppManaged" -> Settings.perAppProxyManagedMode = args["value"] == true
            "allowBypass" -> Settings.allowBypass = args["value"] == true
            "autoRedirect" -> Settings.autoRedirect = args["value"] == true
            "dynamicNotification" -> Settings.dynamicNotification = args["value"] == true
            "disableDeprecatedWarnings" -> Settings.disableDeprecatedWarnings = args["value"] == true
            "oomKillerEnabled" -> Settings.oomKillerEnabled = args["value"] == true
            "oomKillerDisabled" -> Settings.oomKillerDisabled = args["value"] == true
            "oomMemoryLimitMB" -> {
                val value = (args["value"] as Number).toInt()
                require(value in 10..4096) { "Memory limit must be 10–4096 MiB" }
                Settings.oomMemoryLimitMB = value
            }
            "powerReportEnabled" -> Settings.powerReportEnabled = args["value"] == true
            "queryMode" -> {
                val value = args["value"] as String
                require(value in listOf(Settings.PACKAGE_QUERY_MODE_ROOT, Settings.PACKAGE_QUERY_MODE_SHIZUKU))
                PackageQueryManager.setQueryMode(value)
            }
            else -> error("Unknown setting: " + key)
        }
        withContext(Dispatchers.IO) {
            Settings.dataStore.flush()
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

    fun notifyIntent() = emit("intentAvailable", true)

    internal fun emit(type: String, value: Any?) {
        scope.launch(Dispatchers.Main.immediate) { sink?.success(mapOf("type" to type, "value" to value)) }
    }

    override fun close() {
        channel.setMethodCallHandler(null)
        events.setStreamHandler(null)
        streamJob?.cancel()
        sink = null
        if (::dashboard.isInitialized) {
            dashboard.commandClient.removeHandler(rates)
            ProfileManager.unregisterCallback(profileCallback)
            PackageQueryManager.unregisterListeners()
            if (connectionsVisible) connections.setVisible(false)
            toolBridge.close()
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
