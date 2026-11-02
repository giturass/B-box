package io.nekohasekai.sfa.flutter

import android.util.AtomicFile
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.database.ProfileContent
import org.json.JSONObject
import java.io.File
import java.io.FileNotFoundException
import java.security.KeyStore

/** Private application settings, independent of profiles and their service revisions. */
internal class CaptureSettings(private val directory: File, private val retiredKeyAlias: String? = "b_box_http_debug_ca_v1") {
    private val file = AtomicFile(File(directory, "settings.json"))
    private var migrated = false

    private fun read(maxBytes: Int): String? {
        // openRead restores an interrupted write, including a legacy .bak with
        // no base file. Check the recovered descriptor instead of the old path.
        val input = try {
            file.openRead()
        } catch (error: FileNotFoundException) {
            if (file.baseFile.exists() || File(file.baseFile.path + ".bak").exists()) throw error
            return null
        }
        return input.use {
            require(it.channel.size() <= maxBytes) { "Capture settings exceed ${maxBytes / 1024} KiB" }
            it.readBytes().toString(Charsets.UTF_8)
        }
    }

    // Caller holds BoxCapture's process-wide operation mutex.
    private fun raw(): String {
        if (!migrated) {
            AtomicFile(File(directory, "ca.json")).delete()
            if (retiredKeyAlias != null) KeyStore.getInstance("AndroidKeyStore").apply {
                load(null)
                if (containsAlias(retiredKeyAlias)) deleteEntry(retiredKeyAlias)
            }
            val previous = read(4 * 1024 * 1024)
            if (previous != null) {
                val value = JSONObject(previous)
                val retired = listOf("blockQUIC", "rewriteEnabled", "headerRewrite", "urlRewrite", "bodyRewrite", "mapLocal", "scripts",
                    "captureNetwork", "captureTlsKeys", "includeHosts", "excludeHosts", "tlsLibraries", "tlsOffsets")
                if (value.optInt("version") != 2) {
                    retired.forEach(value::remove)
                    // Passive capture consent does not enable active TLS interception.
                    value.put("version", 2).put("https", false)
                    writeCaptureFile(file, Libbox.validateUICaptureConfig(value.toString()))
                }
            }
            migrated = true
        }
        return read(64 * 1024) ?: ""
    }

    fun snapshot(): Map<String, Any?> {
        val raw = raw()
        return mapOf("revision" to ProfileContent.revision(raw),
            "config" to jsonObject(JSONObject(Libbox.validateUICaptureConfig(raw.ifEmpty { "{}" }))))
    }

    fun config(): String = Libbox.validateUICaptureConfig(raw().ifEmpty { "{}" })

    fun save(expectedRevision: String, content: String): Map<String, Any?> {
        require(ProfileContent.revision(raw()) == expectedRevision) { "Capture settings changed; reload them before saving" }
        val validated = Libbox.validateUICaptureConfig(content)
        writeCaptureFile(file, validated)
        return snapshot()
    }
}

internal fun writeCaptureFile(file: AtomicFile, content: String) {
    val parent = file.baseFile.parentFile ?: error("Missing capture directory")
    check(parent.isDirectory || parent.mkdirs()) { "Unable to create capture directory" }
    val stream = file.startWrite()
    try {
        stream.write(content.toByteArray(Charsets.UTF_8))
        file.finishWrite(stream)
    } catch (error: Exception) {
        file.failWrite(stream)
        throw error
    }
}
