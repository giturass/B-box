package io.nekohasekai.sfa.flutter

import android.util.AtomicFile
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.database.ProfileContent
import org.json.JSONObject
import java.io.File
import java.security.KeyStore

/** Private application settings, independent of profiles and their service revisions. */
internal class CaptureSettings(private val directory: File, private val retiredKeyAlias: String? = "b_box_http_debug_ca_v1") {
    private val file = AtomicFile(File(directory, "settings.json"))
    private var migrated = false

    // Caller holds BoxCapture's process-wide operation mutex.
    private fun raw(): String {
        if (!migrated) {
            AtomicFile(File(directory, "ca.json")).delete()
            if (retiredKeyAlias != null) KeyStore.getInstance("AndroidKeyStore").apply {
                load(null)
                if (containsAlias(retiredKeyAlias)) deleteEntry(retiredKeyAlias)
            }
            if (file.baseFile.exists()) {
                require(file.baseFile.length() <= 4 * 1024 * 1024) { "Legacy capture settings exceed 4 MiB" }
                val previous = file.readFully().toString(Charsets.UTF_8)
                val value = JSONObject(previous)
                val retired = listOf("blockQUIC", "rewriteEnabled", "headerRewrite", "urlRewrite", "bodyRewrite", "mapLocal", "scripts")
                if (retired.any(value::has)) {
                    retired.forEach(value::remove)
                    writeCaptureFile(file, Libbox.validateUICaptureConfig(value.toString()))
                }
            }
            migrated = true
        }
        require(!file.baseFile.exists() || file.baseFile.length() <= 64 * 1024) { "Capture settings exceed 64 KiB" }
        return if (file.baseFile.exists()) file.readFully().toString(Charsets.UTF_8) else ""
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
