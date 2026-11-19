package io.nekohasekai.sfa.flutter

import android.content.Context
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.content.pm.Signature
import android.content.pm.SigningInfo
import android.os.Build
import androidx.core.content.pm.PackageInfoCompat
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import java.io.File
import java.security.MessageDigest

internal object UpdateArtifactVerifier {
    data class Identity(val applicationId: String, val versionCode: Long, val version: String, val signers: Set<String>)

    suspend fun verifyFile(file: File, size: Long, digest: String) {
        require(size in 1L until Long.MAX_VALUE && digest.matches(Regex("[0-9a-fA-F]{64}"))) { "Invalid update integrity metadata" }
        require(file.isFile && file.length() == size) { "APK download size does not match release metadata" }
        val sha256 = MessageDigest.getInstance("SHA-256")
        var read = 0L
        file.inputStream().buffered().use { input ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                currentCoroutineContext().ensureActive()
                val bytes = input.read(buffer)
                if (bytes < 0) break
                read += bytes
                require(read <= size) { "APK download exceeds release metadata size" }
                sha256.update(buffer, 0, bytes)
            }
        }
        require(read == size && hex(sha256.digest()).equals(digest, ignoreCase = true)) { "APK checksum does not match release metadata" }
    }

    fun verifyIdentity(installed: Identity, downloaded: Identity, expectedVersionCode: Long, expectedVersion: String) {
        require(downloaded.applicationId == installed.applicationId) { "The downloaded APK belongs to another application" }
        require(downloaded.versionCode == expectedVersionCode && downloaded.version == expectedVersion) { "APK version does not match release metadata" }
        require(downloaded.versionCode > installed.versionCode) { "The downloaded APK is not newer than this installation" }
        require(installed.signers.isNotEmpty() && downloaded.signers.isNotEmpty() && installed.signers == downloaded.signers) {
            "APK signing identity does not match this installation"
        }
    }

    fun hex(bytes: ByteArray): String = bytes.joinToString("") { "%02x".format(it.toInt() and 0xff) }
}

/** Archive identity is checked on every supported Android version; API 36 also exposes signature verification before handoff. */
internal class UpdateApkVerifier(private val context: Context) {
    suspend fun verify(artifact: UpdateDownloadSession.Artifact, file: File) {
        UpdateArtifactVerifier.verifyFile(file, artifact.size, artifact.sha256)
        currentCoroutineContext().ensureActive()
        UpdateArtifactVerifier.verifyIdentity(installedIdentity(), archiveIdentity(file), artifact.versionCode, artifact.version)
    }

    @Suppress("DEPRECATION")
    internal fun installedIdentity(): UpdateArtifactVerifier.Identity = identity(context.packageManager.getPackageInfo(context.packageName, flags()))

    internal fun archiveIdentity(file: File): UpdateArtifactVerifier.Identity {
        val archive = context.packageManager.getPackageArchiveInfo(file.absolutePath, flags()) ?: error("Unable to read the downloaded APK")
        val result = identity(archive)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.BAKLAVA) {
            // Official Android API 36; the current Gradle signing policy produces APK v2 signatures.
            val verified = PackageManager.getVerifiedSigningInfo(file.absolutePath, SigningInfo.VERSION_SIGNING_BLOCK_V2)
            return result.copy(signers = certificateDigests(verified.apkContentsSigners))
        }
        return result
    }

    @Suppress("DEPRECATION")
    private fun flags(): Int = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES

    private fun certificateDigests(signatures: Array<out Signature>?): Set<String> = signatures.orEmpty()
        .map { UpdateArtifactVerifier.hex(MessageDigest.getInstance("SHA-256").digest(it.toByteArray())) }.toSet()

    @Suppress("DEPRECATION")
    private fun identity(info: PackageInfo): UpdateArtifactVerifier.Identity {
        val signatures = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) info.signingInfo?.apkContentsSigners else info.signatures
        return UpdateArtifactVerifier.Identity(
            info.packageName, PackageInfoCompat.getLongVersionCode(info), info.versionName.orEmpty(),
            certificateDigests(signatures),
        )
    }
}
