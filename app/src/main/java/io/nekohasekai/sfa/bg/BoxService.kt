package io.nekohasekai.sfa.bg

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.Uri
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.os.PowerManager
import android.util.Log
import androidx.annotation.RequiresApi
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.MutableLiveData
import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.libbox.CommandServerHandler
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.Notification
import io.nekohasekai.libbox.OverrideOptions
import io.nekohasekai.libbox.PlatformInterface
import io.nekohasekai.libbox.SystemProxyStatus
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.compose.MainActivity
import io.nekohasekai.sfa.constant.Action
import io.nekohasekai.sfa.constant.Alert
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.ProfileManager
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.flutter.MitmCertificate
import io.nekohasekai.sfa.ktx.hasPermission
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import java.io.File
import java.util.TimeZone

class BoxService(private val service: Service, private val platformInterface: PlatformInterface) : CommandServerHandler {
    internal data class DashboardConfig(val server: CommandServer, val profileId: Long, val content: String)

    companion object {
        @Volatile internal var dashboardServer: CommandServer? = null
            private set
        private val _dashboardConfig = MutableStateFlow<DashboardConfig?>(null)
        internal val dashboardConfig = _dashboardConfig.asStateFlow()
        private const val TAG = "BoxService"

        @OptIn(DelicateCoroutinesApi::class)
        fun start() = GlobalScope.launch(Dispatchers.Main.immediate) {
            Settings.dataStore.initialize()
            withContext(Dispatchers.IO) {
                Settings.rebuildServiceMode()
            }
            val intent = Intent(Application.application, Settings.serviceClass())
            ContextCompat.startForegroundService(Application.application, intent)
        }

        fun stop() {
            Application.application.sendBroadcast(
                Intent(Action.SERVICE_CLOSE).setPackage(
                    Application.application.packageName,
                ),
            )
        }
    }

    var fileDescriptor: ParcelFileDescriptor? = null

    private val status = MutableLiveData(Status.Stopped)
    private val binder = ServiceBinder(status)
    private val notification = ServiceNotification(status, service)
    private lateinit var commandServer: CommandServer
    private val idleModeScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val idleModeUpdates = Channel<Boolean>(Channel.UNLIMITED)

    init {
        idleModeScope.launch {
            for (idle in idleModeUpdates) {
                if (idle) {
                    commandServer.pause()
                } else {
                    commandServer.wake()
                }
            }
        }
    }

    private var receiverRegistered = false
    private val receiver =
        object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                when (intent.action) {
                    Action.SERVICE_CLOSE -> {
                        stopService()
                    }

                    PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED -> {
                        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                            serviceUpdateIdleMode()
                        }
                    }
                }
            }
        }

    private fun startCommandServer() {
        Libbox.promoteOOMDraft()
        Libbox.discardPowerReportDraft()
        val commandServer = CommandServer(this, platformInterface)
        commandServer.start()
        this.commandServer = commandServer
        dashboardServer = commandServer
    }

    private var lastProfileName = ""
    private var startupJob: Job? = null
    @Volatile private var stopRequested = false
    @Volatile private var serviceGeneration = 0L

    private suspend fun startService(generation: Long) {
        try {
            val selectedProfileId = Settings.selectedProfile
            if (selectedProfileId == -1L) {
                stopAndAlert(generation, Alert.EmptyConfiguration)
                return
            }

            val profile = ProfileManager.get(selectedProfileId)
            if (profile == null) {
                stopAndAlert(generation, Alert.EmptyConfiguration)
                return
            }

            val content = File(profile.typed.path).readText()
            if (content.isBlank()) {
                stopAndAlert(generation, Alert.EmptyConfiguration)
                return
            }

            lastProfileName = profile.name
            withContext(Dispatchers.Main) {
                notification.show(lastProfileName, R.string.status_starting)
            }

            DefaultNetworkMonitor.start()

            try {
                startOrReloadCore(content, generation, File(profile.typed.path).parent.orEmpty())
            } catch (e: Exception) {
                stopAndAlert(generation, Alert.CreateService, e.message)
                return
            }

            publishDashboardConfig(generation, selectedProfileId, content)
            if (commandServer.needWIFIState()) {
                val wifiPermission =
                    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
                        android.Manifest.permission.ACCESS_FINE_LOCATION
                    } else {
                        android.Manifest.permission.ACCESS_BACKGROUND_LOCATION
                    }
                if (!service.hasPermission(wifiPermission)) {
                    stopAndAlert(generation, Alert.RequestLocationPermission)
                    return
                }
            }

            withContext(Dispatchers.Main) {
                if (stopRequested || generation != serviceGeneration) return@withContext
                notification.show(lastProfileName, R.string.status_started)
                notification.start()
                if (!stopRequested) status.value = Status.Started
            }
        } catch (e: Exception) {
            stopAndAlert(generation, Alert.StartService, e.message)
            return
        }
    }

    override fun serviceStop() {
        notification.close()
        status.postValue(Status.Starting)
        val pfd = fileDescriptor
        if (pfd != null) {
            pfd.close()
            fileDescriptor = null
        }
        closeService()
    }

    override fun serviceReload() {
        runBlocking {
            serviceReload0()
        }
    }

    suspend fun serviceReload0() {
        val generation = serviceGeneration
        val selectedProfileId = Settings.selectedProfile
        if (selectedProfileId == -1L) {
            stopAndAlert(generation, Alert.EmptyConfiguration)
            return
        }

        val profile = ProfileManager.get(selectedProfileId)
        if (profile == null) {
            stopAndAlert(generation, Alert.EmptyConfiguration)
            return
        }

        val content = File(profile.typed.path).readText()
        if (content.isBlank()) {
            stopAndAlert(generation, Alert.EmptyConfiguration)
            return
        }
        lastProfileName = profile.name
        try {
            startOrReloadCore(content, generation, File(profile.typed.path).parent.orEmpty())
        } catch (e: Exception) {
            stopAndAlert(generation, Alert.CreateService, e.message)
            // Return reload failures to the command client so configuration
            // editors can restore their previous content instead of reporting success.
            throw e
        }

        publishDashboardConfig(generation, selectedProfileId, content)
        if (commandServer.needWIFIState()) {
            val wifiPermission =
                if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
                    android.Manifest.permission.ACCESS_FINE_LOCATION
                } else {
                    android.Manifest.permission.ACCESS_BACKGROUND_LOCATION
                }
            if (!service.hasPermission(wifiPermission)) {
                stopAndAlert(generation, Alert.RequestLocationPermission)
                return
            }
        }
    }

    private suspend fun publishDashboardConfig(generation: Long, profileId: Long, content: String) = withContext(Dispatchers.Main) {
        check(!stopRequested && generation == serviceGeneration) { "Service stopped while applying configuration" }
        dashboardServer = commandServer
        _dashboardConfig.value = DashboardConfig(commandServer, profileId, content)
    }

    private fun startOrReloadCore(content: String, generation: Long, profileDirectory: String) = synchronized(MitmCertificate.accessLock) {
        check(!stopRequested && generation == serviceGeneration) { "Service start cancelled" }
        val server = commandServer
        val overrides = OverrideOptions().apply {
            autoRedirect = Settings.autoRedirect
            if (Settings.perAppProxyEnabled) {
                val appList = Settings.getEffectivePerAppProxyList()
                if (Settings.getEffectivePerAppProxyMode() == Settings.PER_APP_PROXY_INCLUDE) {
                    includePackage = PlatformInterfaceWrapper.StringArray((appList + Application.application.packageName).iterator())
                } else {
                    excludePackage = PlatformInterfaceWrapper.StringArray((appList - Application.application.packageName).iterator())
                }
            }
            scriptTimezone = TimeZone.getDefault().id
            processingDirectory = File(service.noBackupFilesDir, "http-debug/captures").absolutePath
            processingProfileDirectory = profileDirectory
            if (Libbox.needsUIMITMCA(content)) {
                val material = MitmCertificate(service).material()
                mitmCertificate = material.first
                mitmSigner = material.second
            }
        }
        // The libbox lifecycle prepares the rules/CA, starts the core, then
        // activates profile processing before reporting startup success.
        server.startOrReloadService(content, overrides)
    }

    override fun getSystemProxyStatus(): SystemProxyStatus? {
        val status = SystemProxyStatus()
        if (service is VPNService) {
            status.available = service.systemProxyAvailable
            status.enabled = service.systemProxyEnabled
        }
        return status
    }

    override fun setSystemProxyEnabled(isEnabled: Boolean) {
        serviceReload()
    }

    @RequiresApi(Build.VERSION_CODES.M)
    private fun serviceUpdateIdleMode() {
        if (::commandServer.isInitialized) {
            idleModeUpdates.trySend(Application.powerManager.isDeviceIdleMode)
        }
    }

    @OptIn(DelicateCoroutinesApi::class)
    // Main-thread entry shared by explicit stops and startup/reload errors.
    // Only this job may tear down the current instance. In particular, an RPC
    // failure can wake the UI's recovery while error handling is still running.
    private fun stopService(alert: Pair<Alert, String?>? = null) {
        if (stopRequested) return
        if (status.value != Status.Started && status.value != Status.Starting) return
        stopRequested = true
        status.value = Status.Stopping
        if (receiverRegistered) {
            service.unregisterReceiver(receiver)
            receiverRegistered = false
        }
        notification.close()
        GlobalScope.launch(Dispatchers.IO) {
            startupJob?.join()
            val pfd = fileDescriptor
            if (pfd != null) {
                pfd.close()
                fileDescriptor = null
            }
            DefaultNetworkMonitor.stop()
            if (::commandServer.isInitialized) {
                closeService()
                runCatching { commandServer.close() }
            }
            Libbox.discardPowerReportDraft()
            PowerReportManager.refresh()
            Settings.startedByUser = false
            Settings.dataStore.flush()
            withContext(Dispatchers.Main) {
                alert?.let { (type, message) ->
                    binder.broadcast { callback -> callback.onServiceAlert(type.ordinal, message) }
                }
                status.value = Status.Stopped
                service.stopSelf()
            }
        }
    }

    private fun closeService() {
        if (dashboardConfig.value?.server === commandServer) _dashboardConfig.value = null
        if (dashboardServer === commandServer) dashboardServer = null
        runCatching {
            commandServer.closeService()
        }.onFailure {
            commandServer.setError("android: close service: ${it.message}")
        }
    }

    private suspend fun stopAndAlert(generation: Long, type: Alert, message: String? = null) {
        withContext(Dispatchers.Main) {
            // Do not join teardown here: this can be the startup job that the
            // stop job must join. Recovery waits for its final Stopped callback.
            if (generation == serviceGeneration) stopService(type to message)
        }
    }

    @OptIn(DelicateCoroutinesApi::class)
    @Suppress("SameReturnValue")
    internal fun onStartCommand(): Int {
        if (status.value != Status.Stopped) return Service.START_NOT_STICKY
        val generation = ++serviceGeneration
        stopRequested = false
        status.value = Status.Starting
        notification.show(lastProfileName, R.string.status_starting)

        if (!receiverRegistered) {
            ContextCompat.registerReceiver(
                service,
                receiver,
                IntentFilter().apply {
                    addAction(Action.SERVICE_CLOSE)
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                        addAction(PowerManager.ACTION_DEVICE_IDLE_MODE_CHANGED)
                    }
                },
                ContextCompat.RECEIVER_NOT_EXPORTED,
            )
            receiverRegistered = true
        }

        startupJob = GlobalScope.launch(Dispatchers.IO) {
            Settings.startedByUser = true
            try {
                Application.application.setupLibbox()
                startCommandServer()
            } catch (e: Exception) {
                stopAndAlert(generation, Alert.StartCommandServer, e.message)
                return@launch
            }
            startService(generation)
        }
        return Service.START_NOT_STICKY
    }

    internal fun onBind(): IBinder = binder

    internal fun onDestroy() {
        if (::commandServer.isInitialized && dashboardConfig.value?.server === commandServer) _dashboardConfig.value = null
        if (::commandServer.isInitialized && dashboardServer === commandServer) dashboardServer = null
        idleModeUpdates.cancel()
        idleModeScope.cancel()
        binder.close()
    }

    internal fun onRevoke() {
        stopService()
    }

    internal fun sendNotification(notification: Notification) {
        val channel = "notification-${notification.typeID}"
        val builder =
            NotificationCompat.Builder(service, channel).setShowWhen(false)
                .setContentTitle(notification.title).setContentText(notification.body)
                .setOnlyAlertOnce(true).setSmallIcon(R.drawable.ic_menu)
                .setCategory(NotificationCompat.CATEGORY_EVENT)
                .setPriority(NotificationCompat.PRIORITY_HIGH).setAutoCancel(true)
        if (!notification.subtitle.isNullOrBlank()) {
            builder.setContentInfo(notification.subtitle)
        }
        if (!notification.openURL.isNullOrBlank()) {
            builder.setContentIntent(
                PendingIntent.getActivity(
                    service,
                    0,
                    Intent(
                        service,
                        MainActivity::class.java,
                    ).apply {
                        setAction(Action.OPEN_URL).setData(Uri.parse(notification.openURL))
                        setFlags(Intent.FLAG_ACTIVITY_REORDER_TO_FRONT)
                    },
                    ServiceNotification.flags,
                ),
            )
        }
        GlobalScope.launch(Dispatchers.Main) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                Application.notification.createNotificationChannel(
                    NotificationChannel(
                        channel,
                        notification.typeName,
                        NotificationManager.IMPORTANCE_HIGH,
                    ),
                )
            }
            Application.notification.notify(notification.identifier, notification.typeID, builder.build())
        }
    }

    internal fun cancelNotification(identifier: String, typeID: Int) {
        GlobalScope.launch(Dispatchers.Main) {
            Application.notification.cancel(identifier, typeID)
        }
    }

    override fun triggerNativeCrash() {
        Thread {
            Thread.sleep(200)
            throw RuntimeException("debug native crash")
        }.start()
    }

    override fun writeDebugMessage(message: String?) {
        Log.d("sing-box", message!!)
    }

    override fun connectSSHAgent(): Int = -1
}
