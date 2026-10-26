package io.nekohasekai.sfa.flutter

import android.content.Context
import android.view.View
import android.widget.FrameLayout
import android.widget.TextView
import io.flutter.plugin.common.StandardMessageCodec
import io.flutter.plugin.platform.PlatformView
import io.flutter.plugin.platform.PlatformViewFactory
import io.nekohasekai.sfa.R
import io.nekohasekai.sfa.compose.screen.tools.OpenConnectWebViewBrowser

internal class BoxBrowserFactory(private val bridge: BoxBridge) : PlatformViewFactory(StandardMessageCodec.INSTANCE) {
    override fun create(context: Context, viewId: Int, args: Any?): PlatformView {
        val params = args as? Map<*, *> ?: emptyMap<String, Any?>()
        val endpoint = params["endpoint"] as? String ?: ""
        val challengeID = params["challenge"] as? String ?: ""
        val container = FrameLayout(context)
        val challenge = bridge.toolBridge.openConnect.endpoint(endpoint)?.authChallenge
        val request = challenge?.browser
        if (challenge?.id != challengeID || request == null) {
            container.addView(TextView(context).apply { text = context.getString(R.string.error_start_service) })
            return object : PlatformView {
                override fun getView(): View = container
                override fun dispose() = Unit
            }
        }
        val browser = OpenConnectWebViewBrowser(
            request,
            if (request.cacheID.isEmpty()) endpoint else request.cacheID + ":" + endpoint,
            context.getString(R.string.endpoint_globalprotect_sso_unsupported_android),
            context.getString(R.string.endpoint_browser_cookie_missing),
            context.getString(R.string.endpoint_browser_profile_unavailable),
            onResult = { result ->
                bridge.toolBridge.openConnect.submitBrowserResponse(endpoint, challengeID, result)
                bridge.emit("browserComplete", challengeID)
            },
            onError = bridge::reportError,
            onDismiss = { bridge.emit("browserComplete", challengeID) },
        )
        browser.start(container)
        return object : PlatformView {
            override fun getView(): View = container
            override fun dispose() = browser.dispose()
        }
    }
}
