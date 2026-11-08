package io.nekohasekai.sfa.flutter

import android.os.Build
import io.nekohasekai.sfa.BuildConfig
import io.nekohasekai.libbox.Libbox
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

/** Uses B-box's release metadata, including prereleases, rather than SFA's updater. */
internal object BoxUpdates {
    private suspend fun get(url: String, timeoutMillis: Long): String = withContext(Dispatchers.IO) {
        // gomobile lowercases the initial uppercase run in UIHTTPGet.
        val response = Libbox.uihttpGetWithTimeout(url, timeoutMillis)
        require(response.status == 200) { "Release request failed: HTTP ${response.status}" }
        response.content.toString(Charsets.UTF_8)
    }

    private val checker = ReleaseChecker(BuildConfig.APPLICATION_ID, loadReleases = { timeout ->
        val releases = JSONArray(get("https://api.github.com/repos/giturass/B-box/releases?per_page=20", timeout))
        (0 until releases.length()).mapNotNull { index ->
            val release = releases.getJSONObject(index)
            if (release.optBoolean("draft")) return@mapNotNull null
            val assets = release.optJSONArray("assets") ?: return@mapNotNull null
            ReleaseChecker.Release(release.getString("html_url"), if (release.isNull("body")) "" else release.optString("body"),
                (0 until assets.length()).map { assetIndex ->
                    val asset = assets.getJSONObject(assetIndex)
                    val url = asset.getString("browser_download_url")
                    ReleaseChecker.Asset(asset.getString("name"), url,
                        "$url:${asset.optLong("id")}:${asset.optString("updated_at")}:${asset.optLong("size")}")
                })
        }
    }, loadMetadata = { asset, timeout ->
        val metadata = JSONObject(get(asset.url, timeout))
        val code = metadata.get("version_code") as? Number ?: error("Invalid release version code")
        val version = metadata.getString("version_name")
        require(code.toLong() > 0L && code.toDouble() == code.toLong().toDouble() && version.isNotBlank()) { "Invalid release version" }
        ReleaseChecker.Metadata(metadata.getString("application_id"), code.toLong(), version)
    })

    suspend fun check(): Map<String, Any?> {
        val result = checker.check()
        val candidate = result.candidate
        val version = candidate?.metadata
        val apk = candidate?.let { item ->
            Build.SUPPORTED_ABIS.firstNotNullOfOrNull { abi ->
                item.release.assets.firstOrNull { it.name == "B-box-${item.metadata.version}-$abi.apk" }
            }
        }
        return mapOf(
            "available" to (version != null && version.versionCode > BuildConfig.VERSION_CODE),
            "hasMetadata" to (candidate != null), "complete" to result.complete,
            "version" to version?.version, "versionCode" to version?.versionCode,
            "url" to (candidate?.release?.url ?: "https://github.com/giturass/B-box/releases"),
            "notes" to candidate?.release?.notes, "downloadUrl" to apk?.url,
        )
    }
}
