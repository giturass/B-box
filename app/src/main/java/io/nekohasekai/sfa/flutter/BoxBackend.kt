package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.backend.tools.OpenConnectBrowserCookieData
import io.nekohasekai.sfa.backend.tools.OpenConnectBrowserResultData
import io.nekohasekai.sfa.flutter.ipc.BackendSession
import io.nekohasekai.sfa.flutter.ipc.IpcPayload
import io.nekohasekai.sfa.vendor.PackageQueryManager
import java.io.Closeable

/** Adapts Flutter's existing application channels to a native host in the default process. */
internal class BoxBackend(private val session: BackendSession) : Closeable {
    private val host = BoxHost(Application.application, waitForUiAttach = true, requestUi = session::requestUi)
    private val bridge = BoxBridge(host, session.messenger)

    init {
        host.reportError = bridge::reportError
        host.notifyIntent = bridge::notifyIntent
        host.prepareBridgeExit = bridge::prepareForExit
        host.resumeBridge = bridge::resume
        host.closeBridge = bridge::close
        host.onClosed = session::completeClose
        session.prepareExitHandler = host::prepareForExit
        session.controlHandler = ::control
    }

    private suspend fun control(method: String, args: Any?): Any? {
        @Suppress("UNCHECKED_CAST")
        val values = (args as? Map<String, Any?>).orEmpty()
        fun string(key: String) = values[key] as? String ?: error("Missing UI argument: $key")
        if (method != "ui.attach") host.awaitUiAttach()
        when (method) {
            "ui.attach" -> host.attachIntentState(args as? String ?: error("Missing Activity identity"))
            "ui.foreground" -> {
                val visible = session.foreground.value
                // Restore permissions and bridge admission before suspended IO.
                host.updateForeground(visible)
                if (visible) {
                    host.recoverIntents()
                    // Dart may have observed resumed while the bridge still
                    // rejected calls. Confirm recovery after stable-ID replay.
                    if (session.foreground.value) bridge.emit("intentRecovery", true)
                }
            }
            "ui.intent" -> host.handleIntent(IpcPayload.unmarshalIntent(args as? ByteArray ?: error("Missing Android intent")))
            "ui.discardState" -> host.discardIntentState()
            "ui.configuration" -> bridge.refreshSystemAppearance()
            "ui.installerPermission" -> if (args == true) BoxUpdates.permissionGranted()
            "ui.error" -> bridge.reportError(args as? String ?: "Android UI operation failed")
            "permission.shizuku" -> return PackageQueryManager.requestShizukuPermission()
            "browser.request" -> return bridge.browserRequest(string("endpoint"), string("challenge"))
            "browser.submit" -> {
                val cookies = (values["cookies"] as? List<*>).orEmpty().map {
                    val cookie = it as? Map<*, *> ?: error("Invalid browser cookie")
                    OpenConnectBrowserCookieData(cookie["name"] as? String ?: error("Missing cookie name"),
                        cookie["value"] as? String ?: error("Missing cookie value"))
                }
                bridge.submitBrowserResult(string("endpoint"), string("challenge"),
                    OpenConnectBrowserResultData(string("finalURL"), cookies))
            }
            "browser.dismiss" -> bridge.emit("browserComplete", mapOf("id" to string("challenge"), "success" to false))
            else -> error("Unknown UI host operation: $method")
        }
        return null
    }

    override fun close() = host.close()
}
