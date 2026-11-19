package io.nekohasekai.sfa.flutter

import org.junit.Assert.*
import org.junit.Test

class UpdateSnapshotTest {
    private val previous = UpdateDownloadSession.Artifact("1.0.2", 750, "B-box-1.0.2-arm64-v8a.apk", "https://example.test/750.apk",
        42, "a".repeat(64), "https://example.test/750", "Previous notes")
    private val checked = mapOf<String, Any?>(
        "version" to "1.0.3", "versionCode" to 751L, "hasMetadata" to true, "complete" to true,
        "canDownload" to true, "downloadUrl" to "https://example.test/751.apk",
    )

    @Test fun aFreshCheckReplacesACancelledOrFailedDownloadCandidate() {
        for (status in listOf("cancelled", "error")) {
            val result = updateSnapshot(checked, UpdateDownloadSession.State(previous, status, error = "Previous attempt failed"), 749)
            assertEquals("1.0.3", result["version"])
            assertEquals(751L, result["versionCode"])
            assertEquals("https://example.test/751.apk", result["downloadUrl"])
            assertEquals(false, result["canInstall"])
        }
    }

    @Test fun aRetainedVerifiedApkKeepsItsOwnVersionAcrossAHostOrCheckRefresh() {
        for (status in listOf("downloading", "verifying", "ready", "permissionRequired", "installing", "error")) {
            val result = updateSnapshot(checked, UpdateDownloadSession.State(previous, status, canInstall = status == "error"), 749)
            assertEquals("1.0.2", result["version"])
            assertEquals(750L, result["versionCode"])
            assertEquals(previous.url, result["downloadUrl"])
        }
    }

    @Test fun aFailureCanStillRetryThePreviouslyConfirmedCandidateWithoutClaimingACompleteCheck() {
        val result = updateSnapshot(mapOf("complete" to false), UpdateDownloadSession.State(previous, "error"), 749)
        assertEquals(750L, result["versionCode"])
        assertEquals(true, result["canDownload"])
        assertEquals(false, result["complete"])
        assertEquals(false, result["canInstall"])
    }
}
