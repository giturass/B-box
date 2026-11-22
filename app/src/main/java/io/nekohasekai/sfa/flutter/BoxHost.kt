package io.nekohasekai.sfa.flutter

import android.Manifest
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.net.Uri
import android.net.VpnService
import android.os.Build
import android.provider.OpenableColumns
import android.util.Log
import androidx.core.content.ContextCompat
import androidx.core.content.IntentCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.lifecycleScope
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.backend.tools.TaildropSendFile
import io.nekohasekai.sfa.backend.tools.TaildropSendManager
import io.nekohasekai.sfa.bg.BoxService
import io.nekohasekai.sfa.bg.ServiceConnection
import io.nekohasekai.sfa.bg.ServiceNotification
import io.nekohasekai.sfa.constant.Action
import io.nekohasekai.sfa.constant.Alert
import io.nekohasekai.sfa.constant.ServiceMode
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.flutter.ipc.IpcPayload
import io.nekohasekai.sfa.ktx.hasPermission
import io.nekohasekai.sfa.utils.AppLifecycleObserver
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import java.io.Closeable
import java.io.File
import java.util.UUID

/** Native state and transactions belong to the service process, never to the Flutter PID. */
internal class BoxHost(
    context: Context,
    waitForUiAttach: Boolean = false,
    private val requestUi: suspend (String, Any?) -> Any?,
) : ContextWrapper(context), LifecycleOwner, ViewModelStoreOwner, ServiceConnection.Callback, Closeable {
    private val registry = LifecycleRegistry(this)
    override val lifecycle: Lifecycle get() = registry
    override val viewModelStore = ViewModelStore()
    val serviceStatus = MutableStateFlow(Status.Stopped)
    private val connection = ServiceConnection(this, this)
    private val connected = CompletableDeferred<Unit>()
    private val foregroundKey = UUID.randomUUID().toString()
    private val uiAttached = CompletableDeferred<Unit>().apply { if (!waitForUiAttach) complete(Unit) }
    private var intentState: BoxIntentState.State? = null
    private var intentStateToken: String? = null
    private val startResult = ServiceStartResult()
    private var startJob: Job? = null
    private var permissionJob: Job? = null
    private var notificationPermissionRequested = false
    private var localNetworkPermissionRequested = false
    private var closing = false
    private var closeInitiated = false
    private var preparingExit = false
    var reportError: (String) -> Unit = {}
    var notifyIntent: () -> Unit = {}
    var prepareBridgeExit: suspend () -> Unit = {}
    var closeBridge: () -> Unit = {}
    var resumeBridge: () -> Unit = {}
    var onClosed: () -> Unit = {}
    private val closeCompleted = CompletableDeferred<Unit>()

    init {
        registry.currentState = Lifecycle.State.CREATED
    }

    private val initialization = lifecycleScope.async {
        uiAttached.await()
        withContext(Dispatchers.IO) {
            Settings.dataStore.initialize()
            Application.application.setupLibbox()
        }
        connection.connect()
        withTimeout(15_000) { connected.await() }
    }

    suspend fun initialize() = initialization.await()

    suspend fun awaitUiAttach() = uiAttached.await()

    suspend fun attachIntentState(token: String) {
        if (intentStateToken == token) return
        check(intentStateToken == null) { "The UI changed its Activity identity" }
        try {
            intentAttachMutex.withLock {
                check(!closeInitiated) { "The native UI host is closing" }
                // A recreated Activity may attach before the previous engine has
                // finished recovery. Serialize their state handover for this token.
                intentHosts[token]?.takeIf { it !== this }?.let { previous ->
                    previous.close()
                    previous.awaitClosed()
                }
                val state = BoxIntentState.attach(this, token)
                check(!closeInitiated) { "The native UI host is closing" }
                intentState = state
                intentStateToken = token
                intentHosts[token] = this
                uiAttached.complete(Unit)
            }
        } catch (error: Exception) {
            uiAttached.completeExceptionally(error)
            throw error
        }
    }

    fun updateForeground(visible: Boolean) {
        if (closing || closeInitiated) return
        if (visible) {
            preparingExit = false
            resumeBridge()
        }
        AppLifecycleObserver.updateUiForeground(foregroundKey, visible)
        registry.currentState = if (visible) Lifecycle.State.RESUMED else Lifecycle.State.CREATED
    }

    suspend fun handleIntent(intent: Intent) {
        uiAttached.await()
        val state = intentState ?: error("The UI has not attached its Activity state")
        val value = if (intent.action in listOf(Intent.ACTION_SEND, Intent.ACTION_SEND_MULTIPLE)) {
            val token = UUID.randomUUID().toString()
            state.shares[token] = IpcPayload.sanitizeIntent(intent)
            "bbox-share:$token"
        } else {
            val uri = intent.data ?: return
            if (intent.action == Action.OPEN_URL) {
                lifecycleScope.launch {
                    runCatching { openActivity(Intent(Intent.ACTION_VIEW, uri)) }
                        .onFailure { reportError(it.message.orEmpty()) }
                }
                return
            }
            uri.toString()
        }
        try {
            state.enqueue(value)
        } finally {
            notifyIntent()
        }
    }

    suspend fun takeIntent(): Map<String, String>? = intentState?.takeIntent()?.toWire()
    suspend fun completeIntent(id: String) { intentState?.completeIntent(id) }
    suspend fun recoverIntents() { intentState?.requeueUnacknowledged() }
    suspend fun discardIntentState() { intentState?.discard() }
    fun discardShare(token: String) { intentState?.shares?.remove(token) }

    fun openShare(token: String): List<TaildropSendFile> {
        val shares = intentState?.shares ?: error("The UI has not attached its Activity state")
        val shared = shares[token] ?: error("Shared content no longer exists")
        val uris = if (shared.action == Intent.ACTION_SEND_MULTIPLE) {
            IntentCompat.getParcelableArrayListExtra(shared, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
        } else listOfNotNull(IntentCompat.getParcelableExtra(shared, Intent.EXTRA_STREAM, Uri::class.java))
        return if (uris.isNotEmpty()) TaildropSendManager.openURIs(uris) else {
            val text = shared.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()?.takeIf { it.isNotBlank() }
                ?: error("No shared content")
            listOf(TaildropSendManager.createTextFile("shared.txt", text))
        }
    }

    suspend fun openActivity(intent: Intent) {
        requestUi("activity.start", IpcPayload.marshalIntent(intent))
    }

    suspend fun requestIgnoreBatteryOptimization() {
        requestUi("permission.battery", null)
    }

    suspend fun pickDocument(saveName: String? = null, mimeType: String = "*/*"): Uri? =
        (requestUi("document.pick", mapOf("name" to saveName, "mimeType" to mimeType)) as? String)?.let(Uri::parse)

    suspend fun installUpdate(file: File): String =
        requestUi("updates.install", file.absolutePath) as? String ?: error("Android installer returned no result")

    suspend fun readClipboard(previousStamp: Long): Map<*, *>? =
        requestUi("clipboard.read", previousStamp) as? Map<*, *>

    suspend fun requestUsbPermission(device: String): Boolean = requestUi("permission.usb", device) == true

    suspend fun requestShizukuPermission(): Boolean = requestUi("permission.shizuku", null) == true

    fun documentName(uri: Uri, fallback: String = "Profile.json"): String {
        require(uri.scheme == "content" || uri.scheme == "file") { "Unsupported document URL" }
        val name = if (uri.scheme == "content") {
            contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                if (it.moveToFirst()) it.getString(0) else null
            }
        } else uri.lastPathSegment
        return name ?: fallback
    }

    fun readDocument(uri: Uri): Pair<String, ByteArray> = documentName(uri) to
        (contentResolver.openInputStream(uri)?.use { it.readBytes() } ?: error("Unable to read document"))

    fun startService() {
        if (closing || startJob?.isActive == true || permissionJob?.isActive == true) return
        if (serviceStatus.value != Status.Stopped) return
        startJob = lifecycleScope.launch {
            try {
                initialize()
                require(Settings.selectedProfile != -1L) { getString(R.string.error_empty_configuration) }
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !ServiceNotification.checkPermission()) {
                    if (!notificationPermissionRequested) {
                        notificationPermissionRequested = true
                        requestUi("permission.notification", null)
                    }
                    if (!ServiceNotification.checkPermission() && Settings.dynamicNotification) {
                        failStart(getString(R.string.notification_permission_required_description))
                        return@launch
                    }
                }
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.CINNAMON_BUN &&
                    !hasPermission(Manifest.permission.ACCESS_LOCAL_NETWORK) && !localNetworkPermissionRequested
                ) {
                    localNetworkPermissionRequested = true
                    requestUi("permission.localNetwork", null)
                }
                withContext(Dispatchers.IO) { Settings.rebuildServiceMode() }
                if (Settings.serviceMode == ServiceMode.VPN) {
                    val permission = VpnService.prepare(this@BoxHost)
                    if (permission != null && requestUi("permission.vpn", IpcPayload.marshalIntent(permission)) != true) {
                        failStart(getString(R.string.error_missing_vpn_permission))
                        return@launch
                    }
                }
                serviceStatus.value = Status.Starting
                ContextCompat.startForegroundService(this@BoxHost, Intent(this@BoxHost, Settings.serviceClass()))
                Settings.startedByUser = true
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                serviceStatus.value = Status.Stopped
                failStart(error.message ?: error.toString())
            }
        }
    }

    fun stopService() {
        startJob?.cancel()
        permissionJob?.cancel()
        startResult.failed(IllegalStateException("Service start cancelled"))
        BoxService.stop()
    }

    suspend fun applyProfile(restart: Boolean = false, recover: Boolean = false) {
        initialize()
        if (!recover && serviceStatus.value in setOf(Status.Starting, Status.Stopping)) {
            withTimeout(15_000) { serviceStatus.first { it == Status.Started || it == Status.Stopped } }
        }
        if (serviceStatus.value != Status.Started && !recover) return
        if (Settings.selectedProfile == -1L) {
            stopService()
            withTimeout(15_000) { serviceStatus.first { it == Status.Stopped } }
            return
        }
        if (!restart && !recover && serviceStatus.value == Status.Started && Settings.detectServiceMode() == Settings.serviceMode) {
            withContext(Dispatchers.IO) { Libbox.newStandaloneCommandClient().serviceReload() }
            check(connection.status == Status.Started && BoxService.currentStatus == Status.Started) {
                "The local service stopped while applying the configuration"
            }
        } else {
            stopService()
            withTimeout(15_000) { serviceStatus.first { it == Status.Stopped } }
            try {
                startResult.awaitStarted { startService() }
            } catch (error: Exception) {
                withContext(NonCancellable) {
                    stopService()
                    runCatching { withTimeout(15_000) { serviceStatus.first { it == Status.Stopped } } }
                        .exceptionOrNull()?.let(error::addSuppressed)
                }
                throw error
            }
        }
    }

    override fun onServiceStatusChanged(status: Status) {
        serviceStatus.value = status
        connected.complete(Unit)
        if (status == Status.Started) startResult.started()
    }

    override fun onServiceAlert(type: Alert, message: String?) {
        if (closing) return
        if (type == Alert.RequestLocationPermission) {
            if (closeInitiated || preparingExit) {
                // Recovery may still restart the previous configuration while
                // closing, but it cannot open a new prompt in the departed UI.
                failStart(getString(R.string.location_permission_description))
                return
            }
            permissionJob = lifecycleScope.launch {
                try {
                    val granted = requestUi("permission.location", null) == true
                    permissionJob = null
                    if (granted) startService() else failStart(getString(R.string.location_permission_description))
                } catch (cancelled: kotlinx.coroutines.CancellationException) {
                    throw cancelled
                } catch (error: Exception) {
                    failStart(error.message ?: error.toString())
                }
            }
            return
        }
        failStart(message ?: getString(when (type) {
            Alert.RequestVPNPermission -> R.string.error_missing_vpn_permission
            Alert.RequestNotificationPermission -> R.string.notification_permission_required_description
            Alert.EmptyConfiguration -> R.string.error_empty_configuration
            Alert.StartCommandServer -> R.string.error_start_command_server
            Alert.CreateService -> R.string.error_create_service
            else -> R.string.error_start_service
        }))
    }

    private fun failStart(message: String) {
        startResult.failed(IllegalStateException(message))
        reportError(message)
    }

    /** The UI waits for this barrier before terminating its PID. */
    suspend fun prepareForExit() {
        preparingExit = true
        // Capture before suspending: an older drain must never cancel a start
        // or permission chain requested after the user returns to the UI.
        val starts = listOfNotNull(startJob, permissionJob)
        starts.forEach { it.cancel() }
        prepareBridgeExit()
        starts.joinAll()
        withContext(Dispatchers.IO) { Settings.dataStore.flush() }
        intentState?.persist()
    }

    override fun close() {
        if (closeInitiated) return
        closeInitiated = true
        if (!uiAttached.isCompleted) uiAttached.cancel()
        AppLifecycleObserver.updateUiForeground(foregroundKey, false)
        lifecycleScope.launch {
            try {
                prepareForExit()
            } catch (error: Exception) {
                Log.w("BoxHost", "Native UI host shutdown", error)
            } finally {
                withContext(NonCancellable) {
                    closing = true
                    fun cleanup(work: () -> Unit) {
                        runCatching(work).onFailure { Log.w("BoxHost", "Native UI host cleanup", it) }
                    }
                    cleanup(closeBridge)
                    cleanup(connection::disconnect)
                    cleanup(viewModelStore::clear)
                    intentStateToken?.let { token ->
                        if (intentHosts[token] === this@BoxHost) intentHosts.remove(token)
                    }
                    try {
                        cleanup { registry.currentState = Lifecycle.State.DESTROYED }
                        cleanup(onClosed)
                    } finally { closeCompleted.complete(Unit) }
                }
            }
        }
    }

    suspend fun awaitClosed() = closeCompleted.await()

    companion object {
        private val intentAttachMutex = Mutex()
        private val intentHosts = mutableMapOf<String, BoxHost>()
    }
}
