package io.nekohasekai.sfa.flutter

import android.content.Context
import android.graphics.Typeface
import android.view.KeyEvent
import android.view.View
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel
import io.flutter.plugin.common.StandardMessageCodec
import io.flutter.plugin.platform.PlatformView
import io.flutter.plugin.platform.PlatformViewFactory
import io.github.sagernet.libghostty.GhosttyExtraKeysState
import io.github.sagernet.libghostty.GhosttyTerminalView
import io.github.sagernet.libghostty.GhosttyTerminalSession
import io.github.sagernet.libghostty.GhosttyUiHandler
import io.github.sagernet.libghostty.extras.GhosttyFontStore
import io.github.sagernet.libghostty.extras.GhosttyThemeStore
import io.nekohasekai.sfa.terminal.GhosttyCustomConfig
import io.nekohasekai.sfa.terminal.TerminalClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** Only the terminal surface is native. Navigation, settings and prompts are Flutter. */
internal class BoxTerminalFactory(private val messenger: BinaryMessenger,
    private val invokeBackend: suspend (String, Map<String, Any?>) -> Any?) : PlatformViewFactory(StandardMessageCodec.INSTANCE) {
    override fun create(context: Context, viewId: Int, args: Any?): PlatformView = TerminalSurface(context, viewId, args, messenger, invokeBackend)
}

private class TerminalSurface(context: Context, id: Int, args: Any?, messenger: BinaryMessenger,
    private val invokeBackend: suspend (String, Map<String, Any?>) -> Any?) : PlatformView {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val channel = MethodChannel(messenger, "io.ericlee.bfa/terminal/$id")
    private val view = GhosttyTerminalView(context)
    private val modifiers = GhosttyExtraKeysState()
    private val client = TerminalClient(context, scope)
    private val updates = Mutex()
    private var updateRevision = 0
    private var attachment: TerminalClient.Attachment? = null
    private var attachedId: String? = null
    private var settings: Map<*, *> = emptyMap<String, Any?>()
    private val pending = mutableSetOf<(Any?) -> Unit>()
    private var disposed = false
    private var sessionId: String? = null
    private var dark = false
    private val preview = (args as? Map<*, *>)?.get("preview") == true
    private val previewSession = if (preview) GhosttyTerminalSession(context).apply {
        feedOutput("\u001b[32muser@b-box\u001b[0m ~\r\n\u001b[36m❯\u001b[0m echo Ghostty\r\nGhostty\r\n\r\n\u001b[1mBold\u001b[0m  \u001b[3mItalic\u001b[0m  \u001b[4mUnderline\u001b[0m\r\n\u001b[31mRed  \u001b[32mGreen  \u001b[34mBlue\u001b[0m\r\n".toByteArray())
    } else null

    init {
        view.extraKeysState = modifiers
        view.showImeOnTap = !preview
        view.uiHandler = object : GhosttyUiHandler {
            override fun confirmUnsafePaste(context: Context, paste: () -> Unit) {
                request("unsafePaste", null) { if (it == true) paste() }
            }
            override fun requestClipboardRead(context: Context, respond: (Boolean) -> Unit) {
                request("clipboardRead", null) { respond(it == true) }
            }
            override fun showLinkMenu(context: Context, url: String, openLink: () -> Unit, copyUrl: () -> Unit) {
                request("link", url) { when (it) { "open" -> openLink(); "copy" -> copyUrl() } }
            }
        }
        view.listener = object : GhosttyTerminalView.Listener {
            override fun onFontSizeChanged(view: GhosttyTerminalView, fontSizeSp: Float) {
                scope.launch {
                    val previous = (settings["fontSize"] as? Number)?.toFloat() ?: 14f
                    try {
                        settings = invokeBackend("terminal.set", mapOf("key" to "fontSize", "value" to fontSizeSp.toInt())) as? Map<*, *>
                            ?: settings
                    } catch (_: Exception) {
                        if (!disposed) view.fontSizeSp = previous
                    }
                }
            }
            override fun onFinishedSessionKey(view: GhosttyTerminalView) {
                val id = sessionId ?: return
                scope.launch { runCatching { invokeBackend("ssh.close", mapOf("id" to id)) } }
            }
        }
        channel.setMethodCallHandler { call, result ->
            scope.launch { try {
                val values = call.arguments as? Map<*, *> ?: emptyMap<String, Any?>()
                when (call.method) {
                    "update" -> update(values)
                    "key" -> {
                        when (values["key"]) {
                            "Ctrl" -> modifiers.toggleCtrl()
                            "Alt" -> modifiers.toggleAlt()
                            "Paste" -> view.pasteFromClipboard()
                            "Keyboard" -> view.showIme()
                            else -> view.sendKey(keyCodes[values["key"]] ?: error("Unknown terminal key"))
                        }
                    }
                    else -> error("Unknown terminal view operation")
                }
                result.success(null)
            } catch (e: Exception) { result.error("terminal", e.message, null) } }
        }
        scope.launch { runCatching { update(args as? Map<*, *> ?: emptyMap<String, Any?>()) } }
        scope.launch { modifiers.ctrl.collect { publishModifiers() } }
        scope.launch { modifiers.alt.collect { publishModifiers() } }
        if (!preview) view.post { if (!disposed) view.showIme() }
    }

    private fun publishModifiers() = channel.invokeMethod("modifiers", mapOf("ctrl" to modifiers.ctrl.value.name, "alt" to modifiers.alt.value.name))

    private suspend fun update(args: Map<*, *>) {
        val revision = ++updateRevision
        updates.withLock {
            if (disposed || revision != updateRevision) return@withLock
            sessionId = args["id"] as? String
            dark = args["dark"] == true
            val currentSettings = invokeBackend("terminal.settings", emptyMap()) as? Map<*, *>
                ?: error("Terminal settings are unavailable")
            if (disposed || revision != updateRevision) return@withLock
            settings = currentSettings
            if (!preview && (attachment == null || attachedId != sessionId)) {
                val previous = attachment
                attachment = null
                attachedId = null
                view.session = null
                previous?.close()
                sessionId?.let { id ->
                    val opened = client.attach(id, ::onAttachmentClosed)
                    if (disposed || revision != updateRevision) {
                        opened.close()
                        return@withLock
                    }
                    attachment = opened
                    attachedId = id
                }
            }
            val session = previewSession ?: attachment?.session
            view.session = session
            fun value(key: String) = settings[key] as? String ?: ""
            val name = value(if (dark) "darkTheme" else "lightTheme")
            val config = if (name.isEmpty()) GhosttyCustomConfig.parse(value(if (dark) "darkConfig" else "lightConfig")) else null
            val theme = config?.theme ?: GhosttyThemeStore.loadThemeOrDefault(view.context, name, dark)
            val appearance = {
                theme?.let { session?.setTheme(it) }
                session?.setColorScheme(dark)
                Unit
            }
            attachment?.setAppearance(dark, appearance) ?: appearance()
            view.typeface = if (settings["fontFollowTheme"] == true) {
                config?.fontFamily?.takeIf { it.isNotBlank() }?.let { Typeface.create(it, Typeface.NORMAL) } ?: Typeface.MONOSPACE
            } else GhosttyFontStore.resolveTypeface(value("fontFamily"), value("fontPath")) ?: Typeface.MONOSPACE
            view.fontSizeSp = (settings["fontSize"] as? Number)?.toFloat() ?: 14f
        }
    }

    private fun onAttachmentClosed(closed: TerminalClient.Attachment) {
        if (attachment !== closed) return
        attachment = null
        attachedId = null
        view.session = null
        if (disposed || sessionId == null) return
        scope.launch {
            delay(100)
            if (!disposed) runCatching { update(mapOf("id" to sessionId, "dark" to dark)) }
        }
    }

    private fun request(method: String, value: Any?, respond: (Any?) -> Unit) {
        if (disposed) { respond(null); return }
        lateinit var finish: (Any?) -> Unit
        finish = { response -> if (pending.remove(finish)) respond(response) }
        pending.add(finish)
        channel.invokeMethod(method, value, object : MethodChannel.Result {
            override fun success(result: Any?) = finish(result)
            override fun error(code: String, message: String?, details: Any?) = finish(null)
            override fun notImplemented() = finish(null)
        })
    }

    override fun getView(): View = view
    override fun dispose() {
        disposed = true
        pending.toList().forEach { it(null) }
        channel.setMethodCallHandler(null)
        view.session = null
        attachment = null
        client.close()
        previewSession?.close()
        view.listener = null
        view.clearFocus()
        scope.cancel()
    }

    companion object {
        private val keyCodes = mapOf("Esc" to KeyEvent.KEYCODE_ESCAPE, "Tab" to KeyEvent.KEYCODE_TAB,
            "↑" to KeyEvent.KEYCODE_DPAD_UP, "↓" to KeyEvent.KEYCODE_DPAD_DOWN,
            "←" to KeyEvent.KEYCODE_DPAD_LEFT, "→" to KeyEvent.KEYCODE_DPAD_RIGHT,
            "Home" to KeyEvent.KEYCODE_MOVE_HOME, "End" to KeyEvent.KEYCODE_MOVE_END,
            "PgUp" to KeyEvent.KEYCODE_PAGE_UP, "PgDn" to KeyEvent.KEYCODE_PAGE_DOWN)
    }
}
