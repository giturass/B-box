package io.nekohasekai.sfa.vendor

import android.os.Build
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.sfa.BuildConfig
import io.nekohasekai.sfa.ktx.unwrap
import io.nekohasekai.sfa.update.UpdateInfo
import io.nekohasekai.sfa.update.UpdateTrack
import io.nekohasekai.sfa.utils.HTTPClient
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.Closeable

class GitHubUpdateChecker : Closeable {
    companion object {
        private const val RELEASES_URL = "https://api.github.com/repos/giturass/sing-box-mod/releases"
        private const val METADATA_FILENAME = "B-box-version-metadata.json"
    }

    private val client = Libbox.newHTTPClient().apply {
        modernTLS()
        keepAlive()
    }

    private val json = Json { ignoreUnknownKeys = true }

    fun checkUpdate(track: UpdateTrack, githubToken: String): UpdateInfo? {
        val request = client.newRequest()
        request.setURL(
            when (track) {
                UpdateTrack.STABLE -> "$RELEASES_URL/latest"
                UpdateTrack.BETA -> "$RELEASES_URL?per_page=3"
            },
        )
        request.setHeader("Accept", "application/vnd.github+json")
        val token = githubToken.trim()
        if (token.isNotEmpty()) {
            request.setHeader("Authorization", "Bearer $token")
        }
        request.setUserAgent(HTTPClient.userAgent)
        val content = request.execute().content.unwrap
        val releases = when (track) {
            UpdateTrack.STABLE -> listOf(json.decodeFromString<GitHubRelease>(content))
            UpdateTrack.BETA -> json.decodeFromString<List<GitHubRelease>>(content)
        }
        val release = releases.filter { !it.draft }.reduceOrNull { best, candidate ->
            if (Libbox.compareSemver(candidate.version, best.version)) candidate else best
        } ?: return null
        if (!Libbox.compareSemver(release.version, BuildConfig.VERSION_NAME)) {
            return null
        }
        val apkAsset = release.findCompatibleApk(
            flavor = BuildConfig.FLAVOR,
            sdkInt = Build.VERSION.SDK_INT,
            supportedAbis = Build.SUPPORTED_ABIS.toList(),
        ) ?: return null
        val metadata = downloadMetadata(release) ?: return null
        if (metadata.applicationId != BuildConfig.APPLICATION_ID ||
            metadata.versionName != release.version ||
            metadata.versionCode <= BuildConfig.VERSION_CODE
        ) {
            return null
        }

        return UpdateInfo(
            versionCode = metadata.versionCode,
            versionName = release.version,
            downloadUrl = apkAsset.browserDownloadUrl,
            releaseUrl = release.htmlUrl,
            releaseNotes = release.body,
            isPrerelease = release.prerelease,
            fileSize = apkAsset.size,
        )
    }

    private fun downloadMetadata(release: GitHubRelease): VersionMetadata? {
        val metadataAsset = release.assets.find { it.name == METADATA_FILENAME }
            ?: return null

        val request = client.newRequest()
        request.setURL(metadataAsset.browserDownloadUrl)
        request.setUserAgent(HTTPClient.userAgent)

        val response = request.execute()
        val content = response.content.unwrap

        return json.decodeFromString<VersionMetadata>(content)
    }

    override fun close() {
        client.close()
    }

    @Serializable
    data class GitHubRelease(
        @SerialName("tag_name") val tagName: String = "",
        val name: String = "",
        val body: String? = null,
        val draft: Boolean = false,
        val prerelease: Boolean = false,
        @SerialName("html_url") val htmlUrl: String = "",
        val assets: List<GitHubAsset> = emptyList(),
    ) {
        val version: String get() = tagName.removePrefix("v")

        internal fun findCompatibleApk(flavor: String, sdkInt: Int, supportedAbis: List<String>): GitHubAsset? {
            if (flavor != "other" || sdkInt < Build.VERSION_CODES.N || "arm64-v8a" !in supportedAbis) {
                return null
            }
            return assets.find { it.name == "B-box-$version-arm64-v8a.apk" }
        }
    }

    @Serializable
    data class GitHubAsset(
        val name: String = "",
        @SerialName("browser_download_url") val browserDownloadUrl: String = "",
        val size: Long = 0,
    )

    @Serializable
    data class VersionMetadata(
        @SerialName("version_code") val versionCode: Int = 0,
        @SerialName("version_name") val versionName: String = "",
        @SerialName("application_id") val applicationId: String = "",
    )
}
