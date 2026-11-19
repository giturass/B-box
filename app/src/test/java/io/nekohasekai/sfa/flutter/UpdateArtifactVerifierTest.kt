package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.security.MessageDigest

class UpdateArtifactVerifierTest {
    @get:Rule val temporary = TemporaryFolder()

    @Test fun fullLengthAndChecksumAreBothRequired() = runTest {
        val data = "APK verification fixture".toByteArray()
        val digest = UpdateArtifactVerifier.hex(MessageDigest.getInstance("SHA-256").digest(data))
        val file = temporary.newFile().apply { writeBytes(data) }
        UpdateArtifactVerifier.verifyFile(file, data.size.toLong(), digest.uppercase())
        assertTrue(runCatching { UpdateArtifactVerifier.verifyFile(file, data.size + 1L, digest) }.isFailure)
        assertTrue(runCatching { UpdateArtifactVerifier.verifyFile(file, data.size - 1L, digest) }.isFailure)
        file.writeBytes(ByteArray(data.size) { 42 })
        assertTrue(runCatching { UpdateArtifactVerifier.verifyFile(file, data.size.toLong(), digest) }.isFailure)
        assertTrue(runCatching { UpdateArtifactVerifier.verifyFile(file, Long.MAX_VALUE, digest) }.isFailure)
        assertTrue(runCatching { UpdateArtifactVerifier.verifyFile(file, data.size.toLong(), "not-a-checksum") }.isFailure)
    }

    @Test fun packageVersionAndAllCurrentSignersMustMatchTheInstalledApplication() {
        val installed = UpdateArtifactVerifier.Identity("io.ericlee.bfa", 749, "1.0.1", setOf("certificate-a", "certificate-b"))
        val candidate = installed.copy(versionCode = 750, version = "1.0.2")
        UpdateArtifactVerifier.verifyIdentity(installed, candidate, 750, "1.0.2")
        for (invalid in listOf(
            candidate.copy(applicationId = "io.nekohasekai.sfa"),
            candidate.copy(versionCode = 751),
            candidate.copy(version = "1.0.3"),
            candidate.copy(signers = setOf("certificate-a")),
            candidate.copy(signers = setOf("certificate-a", "other-certificate")),
            candidate.copy(signers = emptySet()),
        )) assertTrue(runCatching { UpdateArtifactVerifier.verifyIdentity(installed, invalid, 750, "1.0.2") }.isFailure)
        assertTrue(runCatching { UpdateArtifactVerifier.verifyIdentity(installed, installed, 749, "1.0.1") }.isFailure)
        val oldCoreNamedRelease = candidate.copy(versionCode = 748, version = "1.15.0-alpha.13")
        assertTrue(runCatching { UpdateArtifactVerifier.verifyIdentity(installed, oldCoreNamedRelease, 748, "1.15.0-alpha.13") }.isFailure)
        assertTrue(runCatching { UpdateArtifactVerifier.verifyIdentity(installed.copy(signers = emptySet()), candidate, 750, "1.0.2") }.isFailure)
    }
}
