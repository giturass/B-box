package io.nekohasekai.sfa.flutter

import android.os.ParcelFileDescriptor
import io.nekohasekai.sfa.bg.RootClient
import io.nekohasekai.sfa.constant.ServiceMode
import io.nekohasekai.sfa.constant.Status
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.utils.CommandTarget
import io.nekohasekai.sfa.utils.HookErrorClient
import io.nekohasekai.sfa.utils.PrivilegeSettingsClient
import io.nekohasekai.sfa.utils.PrivilegeSettingsConfig
import io.nekohasekai.sfa.utils.VpnDetectionTest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import java.io.File
import kotlin.coroutines.coroutineContext

/** Local Android module controls; no libbox or remote-session state is fabricated. */
internal class BoxPrivilege(private val activity: BoxActivity) {
    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        requireLocal()
        return when (method) {
            "privilege.read" -> PrivilegeSettingsClient.refresh()
            "privilege.save" -> save(args)
            "privilege.logs" -> withContext(Dispatchers.IO) {
                val result = HookErrorClient.query(activity)
                check(result.failure == null) { "Unable to read Hook logs: ${result.failure} ${result.detail.orEmpty()}" }
                result.logs.map { entry -> mapOf(
                    "level" to entry.level, "timestamp" to entry.timestamp, "source" to entry.source,
                    "message" to entry.message, "stackTrace" to entry.stackTrace,
                ) }
            }
            "privilege.test" -> withContext(Dispatchers.IO) {
                val result = VpnDetectionTest.runDetection(activity)
                mapOf(
                    "frameworkDetected" to result.frameworkDetected, "nativeDetected" to result.nativeDetected,
                    "frameworkInterfaces" to result.frameworkInterfaces, "nativeInterfaces" to result.nativeInterfaces,
                    "httpProxy" to result.httpProxy, "package" to activity.packageName,
                )
            }
            "privilege.export" -> export()
            else -> error("Unknown privilege operation")
        }
    }

    private suspend fun save(args: Map<String, Any?>): Map<String, Any?> {
        val next = PrivilegeSettingsConfig.fromWire(args["value"] as? Map<*, *> ?: error("Missing privilege settings"))
        val revision = args["revision"] as? String ?: error("Missing privilege settings revision")
        var restartRequired = false
        try {
            return PrivilegeSettingsClient.update(revision, next, checkReady = {
                withContext(Dispatchers.Main) {
                    requireLocal()
                    check(activity.serviceStatus.value in setOf(Status.Stopped, Status.Started)) {
                        "Wait for the service to finish starting or stopping"
                    }
                    val previous = PrivilegeSettingsClient.read()
                    restartRequired = activity.serviceStatus.value == Status.Started && Settings.serviceMode == ServiceMode.VPN &&
                        (previous.renameInterface != next.renameInterface ||
                            next.renameInterface && previous.interfacePrefix != next.interfacePrefix)
                }
            }) { _, _, recover ->
                if (restartRequired) withContext(Dispatchers.Main) {
                    requireLocal()
                    activity.applyProfile(restart = true, recover = recover)
                }
            }
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            if (error.suppressed.isEmpty()) throw error
            throw IllegalStateException(
                "${error.message}; recovery: ${error.suppressed.joinToString { it.message ?: it.toString() }}", error,
            )
        }
    }

    private suspend fun export(): Boolean {
        val destination = activity.pickDocument("B-box-LSPosed-debug.zip") ?: return false
        requireLocal()
        return withContext(Dispatchers.IO) {
            val directory = File(activity.cacheDir, "lsposed").apply { mkdirs() }
            val file = File.createTempFile("debug-", ".zip", directory)
            try {
                val service = RootClient.bindService()
                ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_WRITE_ONLY or ParcelFileDescriptor.MODE_TRUNCATE).use { output ->
                    service.exportHookDebugInfo(output)
                }
                coroutineContext.ensureActive()
                requireLocal()
                check(file.length() > 0) { "LSPosed diagnostic archive is empty" }
                activity.contentResolver.openOutputStream(destination, "wt")?.use { output ->
                    file.inputStream().use { it.copyTo(output) }
                } ?: error("Unable to save LSPosed diagnostics")
                true
            } finally { file.delete() }
        }
    }

    private fun requireLocal() {
        check(!CommandTarget.isRemote) { "LSPosed settings are only available for the local Android device" }
    }
}
