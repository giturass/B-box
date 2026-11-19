package io.nekohasekai.sfa.flutter

import android.os.Build
import io.nekohasekai.libbox.UIHTTPDownloadProgress
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.BuildConfig
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.URI

/** Uses B-box's application releases and a user-initiated, system-confirmed APK installation. */
internal object BoxUpdates {
    private const val releasesUrl = "https://github.com/giturass/B-box/releases"
    private const val metadataName = "B-box-version-metadata.json"
    private val lock = Any()
    private var confirmed: UpdateDownloadSession.Artifact? = null
    private var checked: Map<String, Any?> = mapOf(
        "available" to false, "hasMetadata" to false, "complete" to false,
        "canDownload" to false, "url" to releasesUrl,
    )
    private val verifier = UpdateApkVerifier(Application.application)
    private val downloads = UpdateDownloadSession(
        CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate),
        File(Application.application.cacheDir, "app-updates"),
        download = { artifact, target, progress ->
            HTTPClient.withTask { task ->
                val response = task.downloadFile(artifact.url, target.absolutePath, artifact.size, object : UIHTTPDownloadProgress {
                    override fun update(downloaded: Long, total: Long) = progress(downloaded, total)
                })
                require(response.status == 200 && response.size == artifact.size && response.digest.equals(artifact.sha256, ignoreCase = true)) {
                    "APK download does not match release metadata"
                }
            }
        },
        verify = verifier::verify,
    )
    val state: StateFlow<UpdateDownloadSession.State> = downloads.state

    private suspend fun get(url: String, timeoutMillis: Long): String = HTTPClient.withTask { task ->
        val response = task.getWithTimeout(url, timeoutMillis)
        require(response.status == 200) { "Release request failed: HTTP ${response.status}" }
        response.content.toString(Charsets.UTF_8)
    }

    private val checker = ReleaseChecker(BuildConfig.APPLICATION_ID, loadReleases = { timeout ->
        val releases = JSONArray(get("https://api.github.com/repos/giturass/B-box/releases?per_page=20", timeout))
        (0 until releases.length()).mapNotNull { index ->
            val release = releases.getJSONObject(index)
            if (release.optBoolean("draft")) return@mapNotNull null
            val assets = if (release.has("assets") && !release.isNull("assets")) release.getJSONArray("assets") else JSONArray()
            val url = release.getString("html_url")
            require(approvedGithubUrl(url, "/giturass/B-box/releases/tag/")) { "Invalid B-box release address" }
            ReleaseChecker.Release(url, if (release.isNull("body")) "" else release.optString("body"),
                (0 until assets.length()).mapNotNull asset@ { assetIndex ->
                    val asset = assets.getJSONObject(assetIndex)
                    val name = asset.getString("name")
                    if (name != metadataName && !(name.startsWith("B-box-") && name.endsWith(".apk"))) return@asset null
                    val assetUrl = asset.getString("browser_download_url")
                    require(name.matches(Regex("[A-Za-z0-9._-]+")) && approvedGithubUrl(assetUrl, "/giturass/B-box/releases/download/") &&
                        URI(assetUrl).rawPath.endsWith("/$name")) { "Invalid B-box release asset" }
                    val size = integer(asset, "size", allowZero = true)
                    ReleaseChecker.Asset(name, assetUrl,
                        "$assetUrl:${integer(asset, "id")}:${asset.optString("updated_at")}:$size", size)
                }, release.getString("tag_name"))
        }
    }, loadMetadata = { asset, timeout -> metadata(JSONObject(get(asset.url, timeout))) })

    internal fun metadata(value: JSONObject): ReleaseChecker.Metadata {
        val version = value.getString("version_name")
        require(version.matches(Regex("[0-9]+\\.[0-9]+\\.[0-9]+(?:-[0-9A-Za-z.-]+)?"))) { "Invalid release version" }
        val rows = if (value.has("artifacts")) value.getJSONArray("artifacts") else JSONArray()
        val artifacts = (0 until rows.length()).map { index ->
            val artifact = rows.getJSONObject(index)
            val name = artifact.getString("name")
            val digest = artifact.getString("sha256")
            require(name.startsWith("B-box-$version-") && name.endsWith(".apk") && name.matches(Regex("[A-Za-z0-9._-]+")) &&
                digest.matches(Regex("[0-9a-fA-F]{64}"))) { "Invalid APK integrity metadata" }
            ReleaseChecker.Artifact(name, digest.lowercase(), integer(artifact, "size"))
        }
        require(artifacts.map { it.name }.toSet().size == artifacts.size) { "Duplicate APK integrity metadata" }
        return ReleaseChecker.Metadata(value.getString("application_id"), integer(value, "version_code"), version, artifacts)
    }

    private fun integer(value: JSONObject, key: String, allowZero: Boolean = false): Long {
        val raw = value.get(key)
        require(raw is Int || raw is Long) { "Invalid release $key" }
        val number = (raw as Number).toLong()
        require(number >= if (allowZero) 0L else 1L) { "Invalid release $key" }
        require(number < Long.MAX_VALUE) { "Invalid release $key" }
        return number
    }

    internal fun approvedGithubUrl(value: String, prefix: String): Boolean = runCatching {
        val url = URI(value)
        url.scheme == "https" && url.host == "github.com" && url.port == -1 && url.rawUserInfo == null &&
            url.rawQuery == null && url.rawFragment == null && url.rawPath.startsWith(prefix) && url.rawPath.length > prefix.length &&
            url.path.split('/').none { it == "." || it == ".." }
    }.getOrDefault(false)

    suspend fun check(): Map<String, Any?> {
        val result = checker.check()
        val candidate = result.candidate
        val version = candidate?.metadata
        val match = candidate?.let { ReleaseChecker.artifact(it, Build.SUPPORTED_ABIS.toList()) }
        val artifact = if (candidate != null && match != null && candidate.release.tag == "v${candidate.metadata.version}") {
            val (asset, integrity) = match
            UpdateDownloadSession.Artifact(candidate.metadata.version, candidate.metadata.versionCode, asset.name, asset.url,
                integrity.size, integrity.sha256, candidate.release.url, candidate.release.notes)
        } else null
        synchronized(lock) {
            confirmed = artifact
            checked = mapOf(
                "available" to (version != null && version.versionCode > BuildConfig.VERSION_CODE),
                "hasMetadata" to (candidate != null), "complete" to result.complete,
                "version" to version?.version, "versionCode" to version?.versionCode,
                "url" to (candidate?.release?.url ?: releasesUrl), "notes" to candidate?.release?.notes,
                "downloadUrl" to artifact?.url, "assetName" to artifact?.name, "size" to artifact?.size,
                "canDownload" to (artifact != null && artifact.versionCode > BuildConfig.VERSION_CODE),
            )
        }
        return snapshot()
    }

    fun snapshot(): Map<String, Any?> {
        return updateSnapshot(synchronized(lock) { checked }, state.value, BuildConfig.VERSION_CODE.toLong())
    }

    fun download(versionCode: Long): Map<String, Any?> {
        val artifact = synchronized(lock) { confirmed?.takeIf { it.versionCode == versionCode } }
            ?: state.value.artifact?.takeIf { it.versionCode == versionCode }
            ?: error("The release changed; check for updates again")
        require(artifact.versionCode > BuildConfig.VERSION_CODE) { "This release is not newer than the installed application" }
        downloads.start(artifact)
        return snapshot()
    }

    suspend fun cancel(): Map<String, Any?> {
        downloads.cancel()
        return snapshot()
    }

    suspend fun install(versionCode: Long, handoff: suspend (File) -> String): Map<String, Any?> {
        downloads.install(versionCode, handoff)
        return snapshot()
    }

    fun permissionGranted() = downloads.permissionGranted()
}
