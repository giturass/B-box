package io.nekohasekai.sfa.flutter

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.content.IntentCompat
import androidx.lifecycle.lifecycleScope
import io.flutter.embedding.android.FlutterFragmentActivity
import io.flutter.embedding.engine.FlutterEngine
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.bg.ServiceConnection
import io.nekohasekai.sfa.bg.ServiceNotification
import io.nekohasekai.sfa.constant.Action
import io.nekohasekai.sfa.constant.Alert
import io.nekohasekai.sfa.constant.ServiceMode
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.ktx.hasPermission
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import java.io.ByteArrayOutputStream

open class BoxActivity : FlutterFragmentActivity(), ServiceConnection.Callback {
    val serviceStatus = MutableStateFlow(Status.Stopped)
    private val connection = ServiceConnection(this, this)
    private var bridge: BoxBridge? = null
    private var notificationPermissionRequested = false
    private var localNetworkPermissionRequested = false
    private var startPending = false
    private var documentRequest: CompletableDeferred<Uri?>? = null
    private val pendingIntents = ArrayDeque<String>()
    private val shares = java.util.concurrent.ConcurrentHashMap<String, Intent>()
    private var launching = false
    private var startGeneration = 0

    private val notificationPermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (startPending && (granted || !Settings.dynamicNotification)) resumeStart() else {
            startPending = false
            onServiceAlert(Alert.RequestNotificationPermission, null)
        }
    }
    private val localNetworkPermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestPermission()) {
        if (startPending) resumeStart()
    }
    private val backgroundLocationPermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted && startPending) resumeStart() else {
            startPending = false
            bridge?.reportError(getString(R.string.location_permission_description))
        }
    }
    private val locationPermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted && startPending) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q && !hasPermission(Manifest.permission.ACCESS_BACKGROUND_LOCATION)) {
                backgroundLocationPermissionLauncher.launch(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
            } else {
                resumeStart()
            }
        } else {
            startPending = false
            bridge?.reportError(getString(R.string.location_permission_description))
        }
    }
    private val vpnPermissionLauncher = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        if (startPending && result.resultCode == RESULT_OK) {
            launchService()
        } else {
            startPending = false
            onServiceAlert(Alert.RequestVPNPermission, null)
        }
    }
    private val openDocument = registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        documentRequest?.complete(uri)
        documentRequest = null
    }
    private val createDocument = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        documentRequest?.complete(if (result.resultCode == RESULT_OK) result.data?.data else null)
        documentRequest = null
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        bridge = BoxBridge(this, flutterEngine.dartExecutor.binaryMessenger)
        flutterEngine.platformViewsController.registry.registerViewFactory("io.ericlee.bfa/openconnect", BoxBrowserFactory(bridge!!))
    }

    override fun cleanUpFlutterEngine(flutterEngine: FlutterEngine) {
        bridge?.close()
        bridge = null
        super.cleanUpFlutterEngine(flutterEngine)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        savedInstanceState?.let { state ->
            notificationPermissionRequested = state.getBoolean("box.notificationPermission")
            localNetworkPermissionRequested = state.getBoolean("box.localNetworkPermission")
            startPending = state.getBoolean("box.startPending")
            state.getStringArrayList("box.intents")?.let { pendingIntents.addAll(it) }
            state.getBundle("box.shares")?.let { bundle ->
                for (key in bundle.keySet()) {
                    androidx.core.os.BundleCompat.getParcelable(bundle, key, Intent::class.java)?.let { shares[key] = it }
                }
            }
        }
        super.onCreate(savedInstanceState)
        lifecycleScope.launch {
            Settings.dataStore.initialize()
            connection.reconnect()
            if (savedInstanceState == null) handleIntent(intent)
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        outState.putBoolean("box.notificationPermission", notificationPermissionRequested)
        outState.putBoolean("box.localNetworkPermission", localNetworkPermissionRequested)
        outState.putBoolean("box.startPending", startPending)
        outState.putStringArrayList("box.intents", ArrayList(pendingIntents))
        outState.putBundle("box.shares", Bundle().apply { shares.forEach { (key, value) -> putParcelable(key, value) } })
        super.onSaveInstanceState(outState)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleIntent(intent)
    }

    private fun handleIntent(intent: Intent?) {
        if (intent == null) return
        val value = if (intent.action in listOf(Intent.ACTION_SEND, Intent.ACTION_SEND_MULTIPLE)) {
            val token = java.util.UUID.randomUUID().toString()
            shares[token] = Intent(intent)
            "bbox-share:" + token
        } else {
            val uri = intent.data ?: return
            if (intent.action == Action.OPEN_URL) {
                runCatching { startActivity(Intent(Intent.ACTION_VIEW, uri)) }.onFailure { bridge?.reportError(it.message.orEmpty()) }
                return
            }
            uri.toString()
        }
        pendingIntents.addLast(value)
        bridge?.notifyIntent()
    }

    fun takeIntent(): String? = pendingIntents.removeFirstOrNull()

    fun discardShare(token: String) { shares.remove(token) }

    fun openShare(token: String): List<io.nekohasekai.sfa.compose.screen.tools.TaildropSendFile> {
        val shared = shares[token] ?: error("Shared content no longer exists")
        val uris = if (shared.action == Intent.ACTION_SEND_MULTIPLE) {
            IntentCompat.getParcelableArrayListExtra(shared, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
        } else listOfNotNull(IntentCompat.getParcelableExtra(shared, Intent.EXTRA_STREAM, Uri::class.java))
        val files = if (uris.isNotEmpty()) {
            io.nekohasekai.sfa.compose.screen.tools.TaildropSendManager.openURIs(uris)
        } else {
            val text = shared.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()?.takeIf { it.isNotBlank() }
                ?: error("No shared content")
            listOf(io.nekohasekai.sfa.compose.screen.tools.TaildropSendManager.createTextFile("shared.txt", text))
        }
        shares.remove(token)
        return files
    }

    fun reconnect() = connection.reconnect()

    private fun resumeStart() {
        startPending = false
        startService()
    }

    @SuppressLint("NewApi")
    fun startService() {
        if (launching || startPending) return
        if (serviceStatus.value == Status.Started || serviceStatus.value == Status.Stopping || serviceStatus.value == Status.Starting) return
        startPending = true
        launching = true
        val generation = startGeneration
        lifecycleScope.launch {
            runCatching {
                Application.application.setupLibbox()
                Settings.dataStore.initialize()
                require(Settings.selectedProfile != -1L) { getString(R.string.error_empty_configuration) }
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !ServiceNotification.checkPermission()) {
                    if (!notificationPermissionRequested) {
                        notificationPermissionRequested = true
                        notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
                        launching = false
                        return@launch
                    }
                    if (Settings.dynamicNotification) {
                        startPending = false
                        onServiceAlert(Alert.RequestNotificationPermission, null)
                        launching = false
                        return@launch
                    }
                }
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.CINNAMON_BUN &&
                    !hasPermission(Manifest.permission.ACCESS_LOCAL_NETWORK) && !localNetworkPermissionRequested
                ) {
                    localNetworkPermissionRequested = true
                    localNetworkPermissionLauncher.launch(Manifest.permission.ACCESS_LOCAL_NETWORK)
                    launching = false
                    return@launch
                }
                withContext(Dispatchers.IO) { Settings.rebuildServiceMode() }
                if (!startPending || generation != startGeneration) { launching = false; return@launch }
                if (Settings.serviceMode == ServiceMode.VPN) {
                    val permission = VpnService.prepare(this@BoxActivity)
                    if (permission != null) {
                        vpnPermissionLauncher.launch(permission)
                        launching = false
                        return@launch
                    }
                }
                launchService()
            }.onFailure {
                startPending = false
                bridge?.reportError(it.message ?: it.toString())
            }
            launching = false
        }
    }

    private fun launchService() {
        if (!startPending) return
        startPending = false
        serviceStatus.value = Status.Starting
        runCatching {
            ContextCompat.startForegroundService(this, Intent(this, Settings.serviceClass()))
            Settings.startedByUser = true
        }.onFailure { serviceStatus.value = Status.Stopped; bridge?.reportError(it.message ?: it.toString()) }
    }

    fun stopService() {
        startGeneration++
        startPending = false
        BoxService.stop()
    }

    suspend fun applyProfile(restart: Boolean = false) {
        if (serviceStatus.value != Status.Started) return
        if (Settings.selectedProfile == -1L) {
            stopService()
            return
        }
        if (!restart && Settings.detectServiceMode() == Settings.serviceMode) {
            withContext(Dispatchers.IO) { Libbox.newStandaloneCommandClient().serviceReload() }
        } else {
            stopService()
            withTimeout(15000) { serviceStatus.first { it == Status.Stopped } }
            withContext(Dispatchers.Main) { startService() }
        }
    }

    suspend fun pickDocument(saveName: String? = null): Uri? {
        check(documentRequest == null) { "A document picker is already open" }
        val request = CompletableDeferred<Uri?>()
        documentRequest = request
        try {
            if (saveName == null) openDocument.launch(arrayOf("*/*")) else createDocument.launch(Intent(Intent.ACTION_CREATE_DOCUMENT).apply {
                addCategory(Intent.CATEGORY_OPENABLE)
                type = java.net.URLConnection.guessContentTypeFromName(saveName) ?: "application/octet-stream"
                putExtra(Intent.EXTRA_TITLE, saveName)
            })
            return request.await()
        } finally {
            if (documentRequest === request) documentRequest = null
        }
    }

    fun readDocument(uri: Uri): Pair<String, ByteArray> {
        require(uri.scheme == "content" || uri.scheme == "file") { "Unsupported document URL" }
        val name = if (uri.scheme == "content") {
            contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                if (it.moveToFirst()) it.getString(0) else null
            }
        } else uri.lastPathSegment
        val bytes = contentResolver.openInputStream(uri)?.use { input ->
            val output = ByteArrayOutputStream()
            val buffer = ByteArray(8192)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                require(output.size() + count <= 16 * 1024 * 1024) { "Document exceeds 16 MiB" }
                output.write(buffer, 0, count)
            }
            output.toByteArray()
        } ?: error("Unable to read document")
        return (name ?: "Profile.json") to bytes
    }

    override fun onServiceStatusChanged(status: Status) {
        serviceStatus.value = status
    }

    override fun onServiceAlert(type: Alert, message: String?) {
        if (type == Alert.RequestLocationPermission) {
            startPending = true
            locationPermissionLauncher.launch(Manifest.permission.ACCESS_FINE_LOCATION)
            return
        }
        startPending = false
        val description = message ?: getString(
            when (type) {
                Alert.RequestVPNPermission -> R.string.error_missing_vpn_permission
                Alert.RequestNotificationPermission -> R.string.notification_permission_required_description
                Alert.EmptyConfiguration -> R.string.error_empty_configuration
                Alert.StartCommandServer -> R.string.error_start_command_server
                Alert.CreateService -> R.string.error_create_service
                else -> R.string.error_start_service
            },
        )
        bridge?.reportError(description)
    }

    override fun onDestroy() {
        documentRequest?.cancel()
        connection.disconnect()
        super.onDestroy()
    }
}
