package io.nekohasekai.sfa.flutter

import android.Manifest
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.IntentFilter
import android.hardware.usb.UsbManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Log
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.FileProvider
import androidx.core.content.ContextCompat
import androidx.lifecycle.lifecycleScope
import io.flutter.embedding.android.FlutterFragmentActivity
import io.flutter.embedding.engine.FlutterEngine
import io.nekohasekai.sfa.flutter.ipc.IpcPayload
import io.nekohasekai.sfa.flutter.ipc.UiBridgeClient
import io.nekohasekai.sfa.ktx.hasPermission
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch
import java.io.File
import java.util.UUID

/** Flutter and Android user interaction live here; native state is held by BoxHost over IPC. */
open class BoxActivity : FlutterFragmentActivity() {
    private var intentStateToken = UUID.randomUUID().toString()
    private var client: UiBridgeClient? = null
    private var documentRequest: CompletableDeferred<Uri?>? = null
    private var permissionRequest: CompletableDeferred<Boolean>? = null
    private var locationRequest: CompletableDeferred<Boolean>? = null
    private var activityRequest: CompletableDeferred<Int>? = null
    private var unknownSourceRequest: CompletableDeferred<Int>? = null

    private val openDocument = registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        documentRequest?.complete(uri)
    }
    private val createDocument = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        documentRequest?.complete(result.data?.data.takeIf { result.resultCode == RESULT_OK })
    }
    private val requestPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        permissionRequest?.complete(granted)
    }
    private val requestLocation = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { granted ->
        locationRequest?.complete(granted[Manifest.permission.ACCESS_FINE_LOCATION] == true ||
            hasPermission(Manifest.permission.ACCESS_FINE_LOCATION))
    }
    private val requestActivity = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        activityRequest?.complete(result.resultCode)
    }
    private val requestUnknownSource = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        unknownSourceRequest?.complete(result.resultCode)
        refreshInstallPermission()
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val messenger = flutterEngine.dartExecutor.binaryMessenger
        val bridge = UiBridgeClient(this, messenger, ::handleUiRequest, lifecycleScope)
        client = bridge
        clients.add(bridge)
        bridge.sendControl("ui.attach", intentStateToken)
        flutterEngine.platformViewsController.registry.registerViewFactory("io.ericlee.bfa/openconnect", BoxBrowserFactory(bridge))
        flutterEngine.platformViewsController.registry.registerViewFactory("io.ericlee.bfa/ghostty", BoxTerminalFactory(messenger, bridge::call))
    }

    override fun cleanUpFlutterEngine(flutterEngine: FlutterEngine) {
        client?.let { bridge ->
            client = null
            clients.remove(bridge)
            val lease = UiProcessLifecycle.current()?.acquireLease()
            val discardState = isFinishing && !isChangingConfigurations
            lateinit var job: Job
            job = processScope.launch(start = CoroutineStart.LAZY) {
                try {
                    bridge.prepareForExit()
                    if (discardState) bridge.callControl("ui.discardState")
                } catch (error: Exception) {
                    Log.w("BoxActivity", "Detached UI bridge shutdown", error)
                } finally {
                    try {
                        runCatching { bridge.close() }.onFailure { Log.w("BoxActivity", "UI bridge cleanup", it) }
                    } finally {
                        closingClients.remove(job)
                        lease?.close()
                    }
                }
            }
            closingClients.add(job)
            job.start()
        }
        super.cleanUpFlutterEngine(flutterEngine)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        intentStateToken = savedInstanceState?.getString(INTENT_STATE_TOKEN) ?: intentStateToken
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) client?.sendIntent(intent)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        outState.putString(INTENT_STATE_TOKEN, intentStateToken)
        super.onSaveInstanceState(outState)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        client?.sendIntent(intent)
    }

    override fun onStart() {
        super.onStart()
        client?.updateForeground(true)
    }

    override fun onStop() {
        client?.updateForeground(false)
        super.onStop()
    }

    override fun onResume() {
        super.onResume()
        refreshInstallPermission()
    }

    override fun onConfigurationChanged(newConfig: android.content.res.Configuration) {
        super.onConfigurationChanged(newConfig)
        client?.sendControl("ui.configuration", null)
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        val lease = UiProcessLifecycle.current()?.takePermissionLease(this, requestCode)
        try {
            super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        } finally { lease?.close() }
    }

    private fun refreshInstallPermission() {
        client?.sendControl("ui.installerPermission", Build.VERSION.SDK_INT < Build.VERSION_CODES.O || packageManager.canRequestPackageInstalls())
    }

    private suspend fun handleUiRequest(method: String, args: Any?): Any? = when (method) {
        "document.pick" -> {
            val values = args as? Map<*, *> ?: error("Missing document request")
            pickDocument(values["name"] as? String, values["mimeType"] as? String ?: "*/*")?.toString()
        }
        "activity.start" -> {
            startActivity(IpcPayload.unmarshalIntent(args as? ByteArray ?: error("Missing Android intent")))
            null
        }
        "permission.notification" -> permission(Manifest.permission.POST_NOTIFICATIONS)
        "permission.localNetwork" -> permission(Manifest.permission.ACCESS_LOCAL_NETWORK)
        "permission.vpn" -> activityResult(IpcPayload.unmarshalIntent(args as? ByteArray ?: error("Missing VPN request"))) == RESULT_OK
        "permission.location" -> locationPermissions()
        "permission.usb" -> usbPermission(args as? String ?: error("Missing USB device"))
        "permission.shizuku" -> externalResult {
            client?.callControl("permission.shizuku") == true
        }
        "updates.install" -> installUpdate(File(args as? String ?: error("Missing verified update")))
        "clipboard.read" -> readClipboard((args as? Number)?.toLong() ?: 0L)
        else -> error("Unknown Android UI operation: $method")
    }

    private suspend fun <T> externalResult(block: suspend () -> T): T {
        val lease = UiProcessLifecycle.current()?.acquireLease()
        try { return block() } finally { lease?.close() }
    }

    private suspend fun pickDocument(name: String?, mimeType: String): Uri? = externalResult {
        check(documentRequest == null) { "A document picker is already open" }
        val request = CompletableDeferred<Uri?>()
        documentRequest = request
        try {
            if (name == null) openDocument.launch(arrayOf(mimeType)) else {
                createDocument.launch(Intent(Intent.ACTION_CREATE_DOCUMENT).apply {
                    addCategory(Intent.CATEGORY_OPENABLE)
                    type = java.net.URLConnection.guessContentTypeFromName(name) ?: "application/octet-stream"
                    putExtra(Intent.EXTRA_TITLE, name)
                })
            }
            request.await()
        } finally { if (documentRequest === request) documentRequest = null }
    }

    private suspend fun permission(name: String): Boolean = externalResult {
        if (hasPermission(name)) return@externalResult true
        check(permissionRequest == null) { "An Android permission request is already open" }
        val request = CompletableDeferred<Boolean>()
        permissionRequest = request
        try {
            requestPermission.launch(name)
            request.await()
        } finally { if (permissionRequest === request) permissionRequest = null }
    }

    private suspend fun locationPermissions(): Boolean = externalResult {
        if (!hasPermission(Manifest.permission.ACCESS_FINE_LOCATION)) {
            check(locationRequest == null) { "A location permission request is already open" }
            val request = CompletableDeferred<Boolean>()
            locationRequest = request
            try {
                requestLocation.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
                if (!request.await()) return@externalResult false
            } finally { if (locationRequest === request) locationRequest = null }
        }
        Build.VERSION.SDK_INT < Build.VERSION_CODES.Q || permission(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
    }

    private suspend fun activityResult(intent: Intent): Int = externalResult {
        check(activityRequest == null) { "An Android activity result is already pending" }
        val request = CompletableDeferred<Int>()
        activityRequest = request
        try {
            requestActivity.launch(intent)
            request.await()
        } finally { if (activityRequest === request) activityRequest = null }
    }

    private suspend fun usbPermission(name: String): Boolean = externalResult {
        val manager = getSystemService(USB_SERVICE) as UsbManager
        val device = manager.deviceList[name] ?: error("USB device disconnected")
        if (manager.hasPermission(device)) return@externalResult true
        val action = "$packageName.action.UI_USB_PERMISSION." + java.util.UUID.randomUUID()
        val request = CompletableDeferred<Boolean>()
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                if (intent.action == action) {
                    request.complete(intent.getBooleanExtra(UsbManager.EXTRA_PERMISSION_GRANTED, false) && manager.hasPermission(device))
                }
            }
        }
        ContextCompat.registerReceiver(this, receiver, IntentFilter(action), ContextCompat.RECEIVER_NOT_EXPORTED)
        try {
            val flags = PendingIntent.FLAG_ONE_SHOT or if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) PendingIntent.FLAG_MUTABLE else 0
            manager.requestPermission(device, PendingIntent.getBroadcast(this, 0, Intent(action).setPackage(packageName), flags))
            request.await()
        } finally { unregisterReceiver(receiver) }
    }

    @Suppress("DEPRECATION")
    private suspend fun installUpdate(file: File): String = externalResult {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !packageManager.canRequestPackageInstalls()) {
            check(unknownSourceRequest == null) { "Android install permission is already open" }
            val request = CompletableDeferred<Int>()
            unknownSourceRequest = request
            try {
                requestUnknownSource.launch(Intent(android.provider.Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:$packageName")))
                request.await()
                return@externalResult if (packageManager.canRequestPackageInstalls()) "ready" else "permissionRequired"
            } finally { if (unknownSourceRequest === request) unknownSourceRequest = null }
        }
        val uri = FileProvider.getUriForFile(this, "$packageName.cache", file)
        val result = activityResult(Intent(Intent.ACTION_INSTALL_PACKAGE).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            clipData = ClipData.newRawUri("B-box update", uri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            putExtra(Intent.EXTRA_RETURN_RESULT, true)
        })
        check(result == RESULT_OK || result == RESULT_CANCELED) { "Android could not install the update" }
        "ready"
    }

    private fun readClipboard(previousStamp: Long): Map<String, Any?>? {
        val clipboard = getSystemService(ClipboardManager::class.java) ?: return null
        val description = clipboard.primaryClipDescription ?: return null
        if (!description.hasMimeType("text/*")) return null
        val stamp = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) description.timestamp else 0L
        if (stamp != 0L && stamp == previousStamp) return null
        val text = clipboard.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.text?.toString()?.trim().orEmpty()
        return mapOf("stamp" to stamp, "text" to text)
    }

    override fun onDestroy() {
        documentRequest?.cancel()
        permissionRequest?.cancel()
        locationRequest?.cancel()
        activityRequest?.cancel()
        unknownSourceRequest?.cancel()
        super.onDestroy()
    }

    companion object {
        private const val INTENT_STATE_TOKEN = "bbox.intentStateToken"
        private val processScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
        private val clients = mutableSetOf<UiBridgeClient>()
        private val closingClients = mutableSetOf<Job>()

        internal suspend fun prepareProcessExit() {
            clients.toList().forEach { it.prepareForExit(backgroundOnly = true) }
            closingClients.toList().joinAll()
        }
    }
}
