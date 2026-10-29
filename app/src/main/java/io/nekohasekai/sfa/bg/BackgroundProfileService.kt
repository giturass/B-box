package io.nekohasekai.sfa.bg

import android.content.Context
import android.content.Intent
import android.net.VpnService
import androidx.core.content.ContextCompat
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.constant.Alert
import io.nekohasekai.sfa.constant.ServiceMode
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.flutter.ServiceStartResult
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout

/** Apply an automatic subscription update to the local service without an Activity. */
internal class BackgroundProfileService(private val context: Context) : ServiceConnection.Callback {
    private val status = MutableStateFlow(Status.Stopped)
    private val connected = CompletableDeferred<Unit>()
    private val startResult = ServiceStartResult()
    private val connection = ServiceConnection(context, this)
    private var connecting = false
    private var recoveryRequired = false

    suspend fun apply(recover: Boolean) = withContext(Dispatchers.Main) {
        if (recover && !recoveryRequired) return@withContext
        if (!recover) recoveryRequired = false
        if (!connecting) {
            connecting = true
            connection.connect()
            withTimeout(15_000) { connected.await() }
        }
        if (!recover) {
            val current = connection.status
            if (current == Status.Stopped) return@withContext
            check(current == Status.Started) { "Wait for the local service to finish starting or stopping" }
        }
        val mode = withContext(Dispatchers.IO) { Settings.detectServiceMode() }
        if (!recover && mode == Settings.serviceMode) {
            recoveryRequired = true
            withContext(Dispatchers.IO) { Libbox.newStandaloneCommandClient().serviceReload() }
            check(connection.status == Status.Started) { "The local service stopped while applying the subscription" }
        } else {
            if (mode == ServiceMode.VPN) {
                check(VpnService.prepare(context) == null) { context.getString(R.string.error_missing_vpn_permission) }
            }
            if (Settings.dynamicNotification) {
                check(ServiceNotification.checkPermission()) { context.getString(R.string.notification_permission_required_description) }
            }
            recoveryRequired = true
            stopAndWait()
            try {
                withContext(Dispatchers.IO) {
                    Settings.rebuildServiceMode()
                    Settings.dataStore.flush()
                }
                startResult.awaitStarted {
                    ContextCompat.startForegroundService(context, Intent(context, Settings.serviceClass()))
                }
            } catch (error: Exception) {
                withContext(NonCancellable) {
                    runCatching { stopAndWait() }.exceptionOrNull()?.let(error::addSuppressed)
                }
                throw error
            }
        }
    }

    private suspend fun stopAndWait() {
        BoxService.stop()
        withTimeout(15_000) { status.first { it == Status.Stopped } }
    }

    suspend fun close() = withContext(NonCancellable + Dispatchers.Main) { connection.disconnect() }

    override fun onServiceStatusChanged(status: Status) {
        this.status.value = status
        connected.complete(Unit)
        if (status == Status.Started) startResult.started()
    }

    override fun onServiceAlert(type: Alert, message: String?) {
        startResult.failed(IllegalStateException(message ?: "Service reported $type"))
    }
}
