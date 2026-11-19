package io.nekohasekai.sfa.flutter

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.nekohasekai.sfa.Application
import io.nekohasekai.sfa.BuildConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.security.MessageDigest
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class UpdateApkVerifierTest {
    @Test fun packageManagerReadsTheActualInstalledArchiveIdentityAndRejectsAnotherPackage() {
        val context = Application.application
        val verifier = UpdateApkVerifier(context)
        val installed = verifier.installedIdentity()
        val archive = verifier.archiveIdentity(File(context.applicationInfo.sourceDir))
        assertEquals(BuildConfig.APPLICATION_ID, installed.applicationId)
        assertEquals(BuildConfig.VERSION_CODE.toLong(), installed.versionCode)
        assertEquals(BuildConfig.VERSION_NAME, installed.version)
        assertTrue(installed.signers.isNotEmpty())
        assertEquals(installed, archive)
        assertTrue(runCatching { UpdateArtifactVerifier.verifyIdentity(installed, archive, installed.versionCode, installed.version) }.isFailure)
        val other = verifier.archiveIdentity(File(InstrumentationRegistry.getInstrumentation().context.applicationInfo.sourceDir))
        assertNotEquals(installed.applicationId, other.applicationId)
        assertTrue(runCatching { UpdateArtifactVerifier.verifyIdentity(installed, other, other.versionCode, other.version) }.isFailure)
    }

    @Test fun matchingHashDoesNotAuthorizeContentThatIsNotAnApk() = runBlocking(Dispatchers.IO) {
        val context = Application.application
        val file = File(context.cacheDir, "update-invalid-${UUID.randomUUID()}.apk")
        try {
            val bytes = "This is not an Android application archive".toByteArray()
            file.writeBytes(bytes)
            val artifact = UpdateDownloadSession.Artifact("1.0.2", BuildConfig.VERSION_CODE + 1L, "B-box-1.0.2-x86_64.apk",
                "https://github.com/giturass/B-box/releases/download/v1.0.2/B-box-1.0.2-x86_64.apk", bytes.size.toLong(),
                UpdateArtifactVerifier.hex(MessageDigest.getInstance("SHA-256").digest(bytes)), "https://github.com/giturass/B-box/releases/tag/v1.0.2", "")
            assertTrue(runCatching { UpdateApkVerifier(context).verify(artifact, file) }.isFailure)
        } finally { file.delete() }
    }

    private fun metadata(): JSONObject = JSONObject()
        .put("application_id", BuildConfig.APPLICATION_ID).put("version_code", 750).put("version_name", "1.0.2")
        .put("artifacts", JSONArray().put(JSONObject().put("name", "B-box-1.0.2-arm64-v8a.apk").put("sha256", "a".repeat(64)).put("size", 42)))

    @Test fun malformedIntegrityAndVersionMetadataCannotAuthorizeADownload() {
        assertEquals(42L, BoxUpdates.metadata(metadata()).artifacts.single().size)
        val legacy = JSONObject().put("application_id", BuildConfig.APPLICATION_ID).put("version_code", 748).put("version_name", "1.15.0-alpha.13")
        assertTrue(BoxUpdates.metadata(legacy).artifacts.isEmpty())
        for ((field, value) in listOf("version_code" to 750.5, "version_code" to -1, "version_name" to "v1.0.2", "artifacts" to JSONObject())) {
            assertTrue("Malformed $field was accepted", runCatching { BoxUpdates.metadata(metadata().put(field, value)) }.isFailure)
        }
        for ((field, value) in listOf("size" to 0, "size" to 42.5, "sha256" to "invalid", "name" to "../B-box-1.0.2-arm64-v8a.apk")) {
            val item = metadata().apply { getJSONArray("artifacts").getJSONObject(0).put(field, value) }
            assertTrue("Malformed artifact $field was accepted", runCatching { BoxUpdates.metadata(item) }.isFailure)
        }
        val duplicate = metadata().apply { getJSONArray("artifacts").put(getJSONArray("artifacts").getJSONObject(0)) }
        assertTrue(runCatching { BoxUpdates.metadata(duplicate) }.isFailure)
    }

    @Test fun downloadAddressesMustBelongToTheConfirmedBboxGithubRepository() {
        val prefix = "/giturass/B-box/releases/download/"
        val path = "${prefix}v1.0.2/B-box-1.0.2-arm64-v8a.apk"
        assertTrue(BoxUpdates.approvedGithubUrl("https://github.com$path", prefix))
        for (url in listOf(
            "http://github.com$path", "https://github.com.example.test$path", "https://example.test$path",
            "https://user:secret@github.com$path", "https://github.com/another/project/releases/download/v1.0.2/update.apk",
            "https://github.com$path?redirect=elsewhere", "https://github.com$path#fragment",
            "https://github.com${prefix}v1.0.2/../update.apk", "https://github.com${prefix}v1.0.2/%2e%2e/update.apk",
        )) assertFalse("Unconfirmed address was accepted: $url", BoxUpdates.approvedGithubUrl(url, prefix))
    }
}
