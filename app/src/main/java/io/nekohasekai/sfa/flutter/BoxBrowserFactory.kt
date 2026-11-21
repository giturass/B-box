package io.nekohasekai.sfa.flutter

import android.content.Context
import android.view.View
import android.widget.FrameLayout
import android.widget.TextView
import io.flutter.plugin.common.StandardMessageCodec
import io.flutter.plugin.platform.PlatformView
import io.flutter.plugin.platform.PlatformViewFactory
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.backend.tools.OpenConnectBrowserRequestData
import io.nekohasekai.sfa.backend.tools.OpenConnectWebViewBrowser
import io.nekohasekai.sfa.flutter.ipc.UiBridgeClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

internal class BoxBrowserFactory(private val bridge: UiBridgeClient) : PlatformViewFactory(StandardMessageCodec.INSTANCE) {
    override fun create(context: Context, viewId: Int, args: Any?): PlatformView {
        val params = args as? Map<*, *> ?: emptyMap<String, Any?>()
        val endpoint = params["endpoint"] as? String ?: ""
        val challengeID = params["challenge"] as? String ?: ""
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
        val container = FrameLayout(context)
        var browser: OpenConnectWebViewBrowser? = null
        scope.launch {
            try {
                val values = bridge.callControl("browser.request", mapOf("endpoint" to endpoint, "challenge" to challengeID)) as? Map<*, *>
                    ?: error("Browser request no longer exists")
                fun string(key: String) = values[key] as? String ?: error("Missing browser field: $key")
                fun strings(key: String) = (values[key] as? List<*>)?.map {
                    it as? String ?: error("Invalid browser field: $key")
                } ?: error("Missing browser field: $key")
                val request = OpenConnectBrowserRequestData(string("url"), string("finalURL"), string("cacheID"),
                    strings("cookieNames"), strings("earlyCookieNames"), strings("headerNames"), strings("callbackURLPrefixes"))
                browser = OpenConnectWebViewBrowser(
                    request,
                    if (request.cacheID.isEmpty()) endpoint else request.cacheID + ":" + endpoint,
                    context.getString(R.string.endpoint_globalprotect_sso_unsupported_android),
                    context.getString(R.string.endpoint_browser_cookie_missing),
                    context.getString(R.string.endpoint_browser_profile_unavailable),
                    onResult = { result ->
                        bridge.sendControl("browser.submit", mapOf("endpoint" to endpoint, "challenge" to challengeID,
                            "finalURL" to result.finalURL, "cookies" to result.cookies.map { it.toWire() }))
                    },
                    onError = { bridge.sendControl("ui.error", it) },
                    onDismiss = { bridge.sendControl("browser.dismiss", mapOf("challenge" to challengeID)) },
                ).also { it.start(container) }
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                container.addView(TextView(context).apply { text = context.getString(R.string.error_start_service) })
                bridge.sendControl("ui.error", error.message.orEmpty())
            }
        }
        return object : PlatformView {
            override fun getView(): View = container
            override fun dispose() {
                scope.cancel()
                browser?.dispose()
                browser = null
            }
        }
    }
}
