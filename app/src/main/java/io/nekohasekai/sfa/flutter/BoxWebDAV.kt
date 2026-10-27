package io.nekohasekai.sfa.flutter

import android.content.SharedPreferences
import io.nekohasekai.libbox.Libbox
import org.json.JSONObject
import java.net.URI

internal class BoxWebDAV(activity: BoxActivity) {
    private val preferences: SharedPreferences = activity.getSharedPreferences("bbox_webdav", 0)

    fun snapshot(): Map<String, Any?>? = preferences.getString("value", null)?.let { jsonObject(JSONObject(it)) }

    fun save(value: Map<String, Any?>?) {
        if (value != null) validate(value)
        check(preferences.edit().putString("value", value?.let { JSONObject(it).toString() }).commit()) { "Unable to save WebDAV account" }
    }

    fun check(): Boolean {
        val props = snapshot() ?: error("Configure a WebDAV account first")
        request(props, "OPTIONS", "")
        return true
    }

    fun upload(content: ByteArray) {
        val props = snapshot() ?: error("Configure a WebDAV account first")
        require(content.size <= BoxBackups.MAX_BYTES) { "Backup exceeds 64 MiB" }
        request(props, "MKCOL", "B-box/", acceptExisting = true)
        request(props, "PUT", "B-box/" + props.getValue("fileName"), content)
    }

    fun download(): ByteArray {
        val props = snapshot() ?: error("Configure a WebDAV account first")
        return request(props, "GET", "B-box/" + props.getValue("fileName"))
    }

    private fun request(props: Map<String, Any?>, method: String, path: String, content: ByteArray = byteArrayOf(), acceptExisting: Boolean = false): ByteArray {
        validate(props)
        val root = URI((props["uri"] as String).trimEnd('/') + "/")
        val url = root.resolve(URI(null, null, path, null, null)).toASCIIString()
        val response = Libbox.uiWebDAVRequest(method, url, props["user"] as String, props["password"] as String, content)
        require(response.status in 200..299 || acceptExisting && response.status == 405) { "WebDAV $method failed: HTTP ${response.status}" }
        if (method == "GET") require(response.status == 200) { "WebDAV download failed: HTTP ${response.status}" }
        return response.content
    }

    companion object {
        fun validate(value: Map<String, Any?>) {
            require(value.keys == setOf("uri", "user", "password", "fileName")) { "Invalid WebDAV account" }
            val address = value["uri"] as? String ?: error("WebDAV address is required")
            val uri = URI(address)
            require(address.length <= 4096 && uri.scheme in setOf("https", "http") && !uri.host.isNullOrBlank() &&
                (uri.port == -1 || uri.port in 1..65535) && uri.userInfo == null && uri.rawQuery == null && uri.fragment == null) {
                "Enter a WebDAV HTTP or HTTPS directory address"
            }
            require((value["user"] as? String)?.length?.let { it <= 4096 } == true &&
                (value["password"] as? String)?.length?.let { it <= 4096 } == true) { "Invalid WebDAV credentials" }
            val file = value["fileName"] as? String ?: error("A backup file name is required")
            require(file.length in 1..128 && file !in setOf(".", "..") && file.none { it == '/' || it == '\\' || it.code < 32 }) { "Invalid backup file name" }
        }
    }
}
