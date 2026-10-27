package io.nekohasekai.sfa.flutter

import android.util.Base64
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

    fun backupSnapshot(): Map<String, Any?> {
        val result = snapshot().filterKeys { it !in setOf("fontPath", "defaultLightTheme", "defaultDarkTheme") }.toMutableMap()
        val path = Settings.tailscaleSSHCustomFontPath
        if (path.isNotEmpty()) {
            val file = File(path)
            require(file.isFile && file.length() <= 16 * 1024 * 1024) { "Imported terminal font is unavailable or too large" }
            result["font"] = mapOf("name" to file.name, "content" to Base64.encodeToString(file.readBytes(), Base64.NO_WRAP))
        }
        return result
    }

    suspend fun validateBackup(value: Map<String, Any?>) {
        val required = setOf("lightTheme", "darkTheme", "lightConfig", "darkConfig", "fontFollowTheme", "fontFamily", "fontSize")
        require(value.keys.containsAll(required) && (value.keys - required - "font").isEmpty()) { "Invalid terminal backup" }
        for (key in listOf("lightTheme", "darkTheme")) {
            val name = value[key] as? String ?: error("Invalid terminal theme")
            require(name.isEmpty() || name in GhosttyThemeStore.listThemes(activity, key == "darkTheme")) { "Unknown Ghostty theme" }
        }
        for (key in listOf("lightConfig", "darkConfig")) require((value[key] as? String)?.length?.let { it <= 256 * 1024 } == true) { "Invalid terminal configuration" }
        require(value["fontFollowTheme"] is Boolean && (value["fontFamily"] as? String)?.length?.let { it <= 128 } == true) { "Invalid terminal font options" }
        val size = value["fontSize"] as? Number ?: error("Invalid terminal font size")
        require(size.toDouble() == size.toInt().toDouble() && size.toInt() in 8..48) { "Font size must be 8–48" }
        value["font"]?.let { font ->
            require(font is Map<*, *> && font.keys == setOf("name", "content")) { "Invalid terminal font backup" }
            val name = font["name"] as? String ?: error("Invalid terminal font name")
            require(name.length in 1..128 && name.none { it == '/' || it == '\\' || it.code < 32 } &&
                name.substringAfterLast('.').lowercase() in setOf("ttf", "otf", "ttc", "otc")) { "Invalid terminal font name" }
            fontBytes(font)
        }
    }

    private fun fontBytes(font: Map<*, *>): ByteArray {
        val content = font["content"] as? String ?: error("Invalid terminal font content")
        require(content.length <= 24 * 1024 * 1024) { "Terminal font exceeds 16 MiB" }
        val bytes = Base64.decode(content, Base64.DEFAULT)
        require(bytes.size <= 16 * 1024 * 1024) { "Terminal font exceeds 16 MiB" }
        return bytes
    }

    suspend fun restoreBackup(value: Map<String, Any?>): File? {
        validateBackup(value)
        var fontFile: File? = null
        try {
            val font = value["font"] as? Map<*, *>
            if (font != null) {
                val name = font["name"] as String
                val extension = name.substringAfterLast('.').lowercase()
                fontFile = File(File(activity.filesDir, "fonts").apply { mkdirs() },
                    "${name.substringBeforeLast('.').take(40)}-${UUID.randomUUID().toString().take(8)}.$extension")
                fontFile.writeBytes(fontBytes(font))
                require(GhosttyFontStore.loadTypeface(fontFile.path) != null) { "Invalid font file" }
            }
            restoreState(value + ("fontPath" to (fontFile?.path ?: "")))
            return fontFile
        } catch (error: Exception) { fontFile?.delete(); throw error }
    }

    suspend fun restoreState(value: Map<String, Any?>) {
        Settings.tailscaleSSHLightTheme = value["lightTheme"] as String
        Settings.tailscaleSSHDarkTheme = value["darkTheme"] as String
        Settings.tailscaleSSHLightConfig = value["lightConfig"] as String
        Settings.tailscaleSSHDarkConfig = value["darkConfig"] as String
        Settings.tailscaleSSHFontFollowTheme = value["fontFollowTheme"] as Boolean
        Settings.tailscaleSSHFontFamily = value["fontFamily"] as String
        Settings.tailscaleSSHCustomFontPath = value["fontPath"] as String
        Settings.tailscaleSSHFontSize = (value["fontSize"] as Number).toInt()
        Settings.dataStore.flush()
    }

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
