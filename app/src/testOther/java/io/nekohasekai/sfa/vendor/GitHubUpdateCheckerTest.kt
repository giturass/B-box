package io.nekohasekai.sfa.vendor

import io.nekohasekai.sfa.vendor.GitHubUpdateChecker.GitHubAsset
import io.nekohasekai.sfa.vendor.GitHubUpdateChecker.GitHubRelease
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class GitHubUpdateCheckerTest {
    @Test
    fun selectsCurrentArm64PackageAmongOtherBuilds() {
        val expected = GitHubAsset(
            name = "B-box-1.15.0-alpha.10-arm64-v8a.apk",
            browserDownloadUrl = "https://example.com/arm64.apk",
        )
        val release = GitHubRelease(
            tagName = "1.15.0-alpha.10",
            assets = listOf(
                GitHubAsset(name = "B-box-1.15.0-alpha.10-x86_64.apk"),
                GitHubAsset(name = "B-box-1.15.0-alpha.10-armeabi-v7a.apk"),
                GitHubAsset(name = "B-box-1.15.0-alpha.10-legacy-android-5-arm64-v8a.apk"),
                GitHubAsset(name = "B-box-1.15.0-alpha.10-play-arm64-v8a.apk"),
                GitHubAsset(name = "SFA-1.15.0-alpha.10-arm64-v8a.apk"),
                GitHubAsset(name = "B-box-1.15.0-alpha.9-arm64-v8a.apk"),
                expected,
            ),
        )

        assertEquals(expected, release.findCompatibleApk("other", 24, listOf("arm64-v8a", "armeabi-v7a")))
    }

    @Test
    fun acceptsVersionTagsWithAndWithoutPrefix() {
        for (tagName in listOf("1.15.0-alpha.10", "v1.15.0-alpha.10")) {
            val release = compatibleRelease().copy(tagName = tagName)
            assertEquals("1.15.0-alpha.10", release.version)
            assertEquals(release.assets.single(), release.findCompatibleApk("other", 24, listOf("arm64-v8a")))
        }
    }

    @Test
    fun unsupportedArchitecturesHaveNoUpdate() {
        val release = compatibleRelease()
        for (abis in listOf(listOf("x86_64", "x86"), listOf("armeabi-v7a"), emptyList())) {
            assertNull(release.findCompatibleApk("other", 36, abis))
        }
    }

    @Test
    fun legacyAndPlayBuildsCannotSelectOtherApk() {
        val release = compatibleRelease()
        assertNull(release.findCompatibleApk("otherLegacy", 36, listOf("arm64-v8a")))
        assertNull(release.findCompatibleApk("play", 36, listOf("arm64-v8a")))
        assertNull(release.findCompatibleApk("other", 23, listOf("arm64-v8a")))
    }

    @Test
    fun missingMatchingApkDoesNotFallBackToReleasePage() {
        val release = compatibleRelease().copy(
            htmlUrl = "https://example.com/releases/1.15.0-alpha.10",
            assets = listOf(GitHubAsset(name = "B-box-version-metadata.json")),
        )
        assertNull(release.findCompatibleApk("other", 24, listOf("arm64-v8a")))
    }

    private fun compatibleRelease() = GitHubRelease(
        tagName = "1.15.0-alpha.10",
        assets = listOf(GitHubAsset(name = "B-box-1.15.0-alpha.10-arm64-v8a.apk")),
    )
}
