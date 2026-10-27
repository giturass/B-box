package io.nekohasekai.sfa.flutter

import io.github.sagernet.libghostty.GhosttyTheme
import io.github.sagernet.libghostty.extras.GhosttyFontStore
import io.github.sagernet.libghostty.extras.GhosttyThemeStore
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.terminal.GhosttyCustomConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.util.UUID

internal class BoxTerminalSettings(private val activity: BoxActivity) {
    fun snapshot(): Map<String, Any?> = mapOf(
        "lightTheme" to Settings.tailscaleSSHLightTheme, "darkTheme" to Settings.tailscaleSSHDarkTheme,
        "lightConfig" to Settings.tailscaleSSHLightConfig, "darkConfig" to Settings.tailscaleSSHDarkConfig,
        "fontFollowTheme" to Settings.tailscaleSSHFontFollowTheme, "fontFamily" to Settings.tailscaleSSHFontFamily,
        "fontPath" to Settings.tailscaleSSHCustomFontPath, "fontSize" to Settings.tailscaleSSHFontSize,
        "defaultLightTheme" to GhosttyThemeStore.DEFAULT_LIGHT_THEME, "defaultDarkTheme" to GhosttyThemeStore.DEFAULT_DARK_THEME,
    )

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "terminal.settings" -> return snapshot()
            "terminal.themes" -> return withContext(Dispatchers.IO) {
                GhosttyThemeStore.listThemes(activity, args["dark"] == true).map { name ->
                    mapOf("name" to name) + themeWire(GhosttyThemeStore.loadTheme(activity, name))
                }
            }
            "terminal.preview" -> return withContext(Dispatchers.IO) {
                val dark = args["dark"] == true
                val custom = args["config"] as? String
                themeWire(if (custom != null) GhosttyCustomConfig.parse(custom).theme
                    else GhosttyThemeStore.loadThemeOrDefault(activity, args["theme"] as? String ?: "", dark))
            }
            "terminal.fonts" -> return withContext(Dispatchers.IO) {
                mapOf("system" to listOf("monospace", "Droid Sans Mono", "Courier New", "Courier"),
                    "imported" to GhosttyFontStore.listImportedFonts(activity).map { mapOf("name" to it.name, "path" to it.path) })
            }
            "terminal.importFont" -> {
                val uri = activity.pickDocument() ?: return null
                return withContext(Dispatchers.IO) {
                    val (name, bytes) = activity.readDocument(uri)
                    val extension = name.substringAfterLast('.', "").lowercase()
                    require(extension in setOf("ttf", "otf", "ttc", "otc")) { "Select a TTF, OTF, TTC or OTC font" }
                    val safeName = name.substringBeforeLast('.').replace(Regex("[^\\p{L}\\p{N} ._-]"), "_").take(80).ifBlank { "Font" }
                    val file = File(File(activity.filesDir, "fonts").apply { mkdirs() }, "$safeName-${UUID.randomUUID().toString().take(8)}.$extension")
                    try {
                        file.writeBytes(bytes)
                        require(GhosttyFontStore.loadTypeface(file.path) != null) { "Invalid font file" }
                    } catch (e: Exception) { file.delete(); throw e }
                    Settings.tailscaleSSHCustomFontPath = file.path
                    Settings.tailscaleSSHFontFamily = ""
                    Settings.tailscaleSSHFontFollowTheme = false
                    snapshot()
                }
            }
            "terminal.deleteFont" -> withContext(Dispatchers.IO) {
                val font = GhosttyFontStore.listImportedFonts(activity).firstOrNull { it.path == args["path"] } ?: error("Font no longer exists")
                check(File(font.path).delete()) { "Unable to delete font" }
                if (Settings.tailscaleSSHCustomFontPath == font.path) Settings.tailscaleSSHCustomFontPath = ""
            }
            "terminal.set" -> {
                val value = args["value"]
                when (args["key"]) {
                    "lightTheme", "darkTheme" -> {
                        val name = value as? String ?: error("Invalid theme")
                        val dark = args["key"] == "darkTheme"
                        require(name.isEmpty() || withContext(Dispatchers.IO) { name in GhosttyThemeStore.listThemes(activity, dark) }) { "Unknown Ghostty theme" }
                        if (dark) Settings.tailscaleSSHDarkTheme = name else Settings.tailscaleSSHLightTheme = name
                    }
                    "lightConfig", "darkConfig" -> {
                        val config = value as? String ?: error("Invalid configuration")
                        require(config.length <= 256 * 1024) { "Configuration is too large" }
                        if (args["key"] == "darkConfig") Settings.tailscaleSSHDarkConfig = config else Settings.tailscaleSSHLightConfig = config
                    }
                    "fontFollowTheme" -> Settings.tailscaleSSHFontFollowTheme = value as? Boolean ?: error("Invalid font option")
                    "fontSize" -> {
                        val size = value as? Number ?: error("Invalid font size")
                        require(size.toDouble() == size.toInt().toDouble() && size.toInt() in 8..48) { "Font size must be 8–48" }
                        Settings.tailscaleSSHFontSize = size.toInt()
                    }
                    "fontFamily" -> {
                        val name = value as? String ?: error("Invalid font")
                        require(name.length <= 128) { "Invalid font" }
                        Settings.tailscaleSSHFontFamily = name
                        Settings.tailscaleSSHCustomFontPath = ""
                        Settings.tailscaleSSHFontFollowTheme = false
                    }
                    "fontPath" -> {
                        val path = value as? String ?: error("Invalid font")
                        require(withContext(Dispatchers.IO) { GhosttyFontStore.listImportedFonts(activity).any { it.path == path } }) { "Unknown imported font" }
                        Settings.tailscaleSSHCustomFontPath = path
                        Settings.tailscaleSSHFontFamily = ""
                        Settings.tailscaleSSHFontFollowTheme = false
                    }
                    else -> error("Unknown terminal setting")
                }
            }
            else -> error("Unknown terminal setting operation")
        }
        return snapshot()
    }

    private fun themeWire(theme: GhosttyTheme?): Map<String, Any?> = mapOf(
        "foreground" to theme?.foreground?.toLong()?.and(0xffffffffL),
        "background" to theme?.background?.toLong()?.and(0xffffffffL),
        "palette" to theme?.palette?.take(16)?.map { it?.toLong()?.and(0xffffffffL) },
    )
}
