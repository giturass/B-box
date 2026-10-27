package io.nekohasekai.sfa.flutter

import android.os.Build
import io.nekohasekai.sfa.BuildConfig
import io.nekohasekai.libbox.Libbox
import org.json.JSONArray
import org.json.JSONObject

/** Uses B-box's release metadata, including prereleases, rather than SFA's updater. */
internal object BoxUpdates {
    private fun get(url: String): String {
        // gomobile lowercases the initial uppercase run in UIHTTPGet.
        val response = Libbox.uihttpGet(url)
        require(response.status == 200) { "Release request failed: HTTP ${response.status}" }
        return response.content.toString(Charsets.UTF_8)
    }

    fun check(): Map<String, Any?> {
        val releases = JSONArray(get("https://api.github.com/repos/giturass/B-box/releases?per_page=20"))
        var latestCode = -1L
        var latest: Map<String, Any?>? = null
        for (index in 0 until releases.length()) {
            val release = releases.getJSONObject(index)
            if (release.optBoolean("draft")) continue
            val assets = release.optJSONArray("assets") ?: continue
            val assetList = (0 until assets.length()).map { assets.getJSONObject(it) }
            val metadataAsset = assetList.firstOrNull { it.optString("name") == "B-box-version-metadata.json" } ?: continue
            val metadata = JSONObject(get(metadataAsset.getString("browser_download_url")))
            if (metadata.optString("application_id") != BuildConfig.APPLICATION_ID) continue
            val code = metadata.optLong("version_code", -1L)
            val version = metadata.optString("version_name")
            if (code <= 0 || code <= latestCode || version.isBlank()) continue
            latestCode = code
            val apk = Build.SUPPORTED_ABIS.firstNotNullOfOrNull { abi ->
                assetList.firstOrNull { it.optString("name") == "B-box-$version-$abi.apk" }
            }
            latest = mapOf(
                "available" to (code > BuildConfig.VERSION_CODE), "hasMetadata" to true,
                "version" to version, "versionCode" to code,
                "url" to release.getString("html_url"), "notes" to if (release.isNull("body")) "" else release.optString("body"),
                "downloadUrl" to apk?.getString("browser_download_url"),
            )
        }
        return latest ?: mapOf("available" to false, "hasMetadata" to false, "url" to "https://github.com/giturass/B-box/releases")
    }
}
