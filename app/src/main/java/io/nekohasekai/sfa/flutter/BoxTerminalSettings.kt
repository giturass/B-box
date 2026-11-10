package io.nekohasekai.sfa.flutter

import android.util.Base64
import io.github.sagernet.libghostty.GhosttyTheme
import io.github.sagernet.libghostty.extras.GhosttyFontStore
import io.github.sagernet.libghostty.extras.GhosttyThemeStore
import io.nekohasekai.sfa.constant.SettingsKey
import io.nekohasekai.sfa.database.Settings
import io.nekohasekai.sfa.database.preference.KeyValueEntity
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
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
            require(file.isFile) { "Imported terminal font is unavailable" }
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
        for (key in listOf("lightConfig", "darkConfig")) require(value[key] is String) { "Invalid terminal configuration" }
        require(value["fontFollowTheme"] is Boolean && value["fontFamily"] is String) { "Invalid terminal font options" }
        val size = value["fontSize"] as? Number ?: error("Invalid terminal font size")
        require(size.toDouble() == size.toInt().toDouble() && size.toInt() in 8..48) { "Font size must be 8–48" }
        value["font"]?.let { font ->
            require(font is Map<*, *> && font.keys == setOf("name", "content")) { "Invalid terminal font backup" }
            val name = font["name"] as? String ?: error("Invalid terminal font name")
            require(name.isNotEmpty() && name.none { it == '/' || it == '\\' || it.code < 32 } &&
                name.substringAfterLast('.').lowercase() in setOf("ttf", "otf", "ttc", "otc")) { "Invalid terminal font name" }
            fontBytes(font)
        }
    }

    private fun fontBytes(font: Map<*, *>): ByteArray {
        val content = font["content"] as? String ?: error("Invalid terminal font content")
        return Base64.decode(content, Base64.DEFAULT)
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
        save(value.filterKeys { it in settingKeys })
    }

    private val settingKeys = mapOf(
        "lightTheme" to SettingsKey.TAILSCALE_SSH_LIGHT_THEME, "darkTheme" to SettingsKey.TAILSCALE_SSH_DARK_THEME,
        "lightConfig" to SettingsKey.TAILSCALE_SSH_LIGHT_CONFIG, "darkConfig" to SettingsKey.TAILSCALE_SSH_DARK_CONFIG,
        "fontFollowTheme" to SettingsKey.TAILSCALE_SSH_FONT_FOLLOW_THEME, "fontFamily" to SettingsKey.TAILSCALE_SSH_FONT_FAMILY,
        "fontPath" to SettingsKey.TAILSCALE_SSH_CUSTOM_FONT_PATH, "fontSize" to SettingsKey.TAILSCALE_SSH_FONT_SIZE,
    )

    private suspend fun save(values: Map<String, Any?>) {
        Settings.dataStore.putBatchConfirmed(values.map { (key, value) ->
            val entity = KeyValueEntity(settingKeys.getValue(key))
            when (key) {
                "fontFollowTheme" -> entity.put(value as Boolean)
                "fontSize" -> entity.put((value as Number).toLong())
                else -> entity.put(value as String)
            }
        })
    }

    suspend fun invoke(method: String, args: Map<String, Any?>): Any? {
        when (method) {
            "terminal.settings" -> return snapshot()
            "terminal.themes" -> return withContext(Dispatchers.IO) {
                GhosttyThemeStore.listThemes(activity, args["dark"] == true).map { name ->
                    mapOf("name" to name) + themeWire(GhosttyThemeStore.loadTheme(activity, name))
                }
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
                        save(mapOf("fontPath" to file.path, "fontFamily" to "", "fontFollowTheme" to false))
                    } catch (e: Exception) { file.delete(); throw e }
                    snapshot()
                }
            }
            "terminal.deleteFont" -> withContext(Dispatchers.IO) {
                val font = GhosttyFontStore.listImportedFonts(activity).firstOrNull { it.path == args["path"] } ?: error("Font no longer exists")
                val selected = Settings.tailscaleSSHCustomFontPath == font.path
                if (selected) save(mapOf("fontPath" to ""))
                try {
                    check(File(font.path).delete()) { "Unable to delete font" }
                } catch (error: Exception) {
                    if (selected) withContext(NonCancellable) {
                        runCatching { save(mapOf("fontPath" to font.path)) }.exceptionOrNull()?.let(error::addSuppressed)
                    }
                    throw error
                }
            }
            "terminal.set" -> {
                val value = args["value"]
                val key = args["key"] as? String ?: error("Missing terminal setting")
                val values = when (key) {
                    "lightTheme", "darkTheme" -> {
                        val name = value as? String ?: error("Invalid theme")
                        val dark = args["key"] == "darkTheme"
                        require(name.isEmpty() || withContext(Dispatchers.IO) { name in GhosttyThemeStore.listThemes(activity, dark) }) { "Unknown Ghostty theme" }
                        mapOf(key to name)
                    }
                    "lightConfig", "darkConfig" -> {
                        val config = value as? String ?: error("Invalid configuration")
                        mapOf(key to config)
                    }
                    "fontFollowTheme" -> mapOf(key to (value as? Boolean ?: error("Invalid font option")))
                    "fontSize" -> {
                        val size = value as? Number ?: error("Invalid font size")
                        require(size.toDouble() == size.toInt().toDouble() && size.toInt() in 8..48) { "Font size must be 8–48" }
                        mapOf(key to size.toInt())
                    }
                    "fontFamily" -> {
                        val name = value as? String ?: error("Invalid font")
                        mapOf(key to name, "fontPath" to "", "fontFollowTheme" to false)
                    }
                    "fontPath" -> {
                        val path = value as? String ?: error("Invalid font")
                        require(withContext(Dispatchers.IO) { GhosttyFontStore.listImportedFonts(activity).any { it.path == path } }) { "Unknown imported font" }
                        mapOf(key to path, "fontFamily" to "", "fontFollowTheme" to false)
                    }
                    else -> error("Unknown terminal setting")
                }
                save(values)
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
