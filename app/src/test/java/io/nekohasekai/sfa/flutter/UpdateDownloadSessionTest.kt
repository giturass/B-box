package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException
import java.security.MessageDigest

@OptIn(ExperimentalCoroutinesApi::class)
class UpdateDownloadSessionTest {
    @get:Rule val temporary = TemporaryFolder()
    private val bytes = "verified APK fixture".toByteArray()
    private fun artifact(code: Long = 750) = UpdateDownloadSession.Artifact(
        "1.0.${code - 748}", code, "B-box-fixture.apk", "https://example.test/$code.apk", bytes.size.toLong(),
        UpdateArtifactVerifier.hex(MessageDigest.getInstance("SHA-256").digest(bytes)), "https://example.test/releases/$code", "Notes",
    )
    private suspend fun verify(artifact: UpdateDownloadSession.Artifact, file: File) = UpdateArtifactVerifier.verifyFile(file, artifact.size, artifact.sha256)

    @Test fun downloadStreamsProgressAndOnlyExposesAFullyVerifiedApk() = runTest {
        val directory = temporary.newFolder()
        var verified = 0
        val session = UpdateDownloadSession(this, directory, { _, file, progress ->
            file.writeBytes(bytes)
            progress(bytes.size.toLong(), bytes.size.toLong())
        }, { artifact, file -> verified++; verify(artifact, file) }, StandardTestDispatcher(testScheduler))
        assertEquals("downloading", session.start(artifact()).status)
        assertFalse(session.state.value.canInstall)
        advanceUntilIdle()
        assertEquals("ready", session.state.value.status)
        assertTrue(session.state.value.canInstall)
        assertEquals(bytes.size.toLong(), session.state.value.downloaded)
        assertEquals(1, verified)
        assertEquals(1, directory.listFiles()!!.size)
        var handedOff: File? = null
        val result = session.install(750) { file -> handedOff = file; "ready" }
        assertEquals("ready", result.status)
        assertEquals(2, verified) // Revalidate immediately before passing the private file to Android.
        assertTrue(handedOff!!.name.endsWith(".apk"))
        assertArrayEquals(bytes, handedOff!!.readBytes())
        assertTrue(result.canInstall)
    }

    @Test fun cancellingAnInFlightDownloadClosesItsWorkAndRemovesThePartialFile() = runTest {
        val directory = temporary.newFolder()
        val requested = CompletableDeferred<Unit>()
        val never = CompletableDeferred<Unit>()
        var stopped = false
        var oldProgress: ((Long, Long) -> Unit)? = null
        var attempt = 0
        val session = UpdateDownloadSession(this, directory, { _, file, progress ->
            attempt++
            file.writeBytes(if (attempt == 1) byteArrayOf(1, 2, 3) else bytes)
            if (attempt == 1) {
                oldProgress = progress
                requested.complete(Unit)
                try { never.await() } finally { stopped = true }
            }
        }, ::verify, StandardTestDispatcher(testScheduler))
        session.start(artifact())
        requested.await()
        assertTrue(runCatching { session.start(artifact(751)) }.isFailure)
        val cancelled = session.cancel()
        assertTrue(stopped)
        assertEquals("cancelled", cancelled.status)
        assertFalse(cancelled.canInstall)
        assertEquals(0, directory.listFiles()!!.size)
        assertTrue(runCatching { session.install(750) { error("A cancelled APK cannot be installed") } }.isFailure)
        session.start(artifact(751))
        oldProgress!!(99, 99)
        assertEquals(0L, session.state.value.downloaded)
        advanceUntilIdle()
        assertEquals("ready", session.state.value.status)
        assertEquals(751L, session.state.value.artifact?.versionCode)
    }

    @Test fun cancellationBeforeTheNetworkJobStartsStillAllowsRetry() = runTest {
        val directory = temporary.newFolder()
        var requests = 0
        val session = UpdateDownloadSession(this, directory, { _, file, _ -> requests++; file.writeBytes(bytes) }, ::verify, StandardTestDispatcher(testScheduler))
        session.start(artifact())
        session.cancel()
        assertEquals(0, requests)
        assertEquals("cancelled", session.state.value.status)
        session.start(artifact())
        advanceUntilIdle()
        assertEquals(1, requests)
        assertEquals("ready", session.state.value.status)
    }

    @Test fun networkAndVerificationFailuresRemoveFilesAndAllowAnExplicitRetry() = runTest {
        val directory = temporary.newFolder()
        var attempts = 0
        var verifications = 0
        val session = UpdateDownloadSession(this, directory, { _, file, _ ->
            file.writeBytes(bytes)
            if (++attempts == 1) throw IOException("Interrupted download")
        }, { artifact, file -> verifications++; verify(artifact, file) }, StandardTestDispatcher(testScheduler))
        session.start(artifact())
        advanceUntilIdle()
        assertEquals("error", session.state.value.status)
        assertEquals("Interrupted download", session.state.value.error)
        assertFalse(session.state.value.canInstall)
        assertEquals(0, verifications)
        assertEquals(0, directory.listFiles()!!.size)
        session.start(artifact().copy(sha256 = "0".repeat(64)))
        advanceUntilIdle()
        assertEquals("error", session.state.value.status)
        assertFalse(session.state.value.canInstall)
        assertEquals(0, directory.listFiles()!!.size)
        session.start(artifact())
        advanceUntilIdle()
        assertEquals("ready", session.state.value.status)
        assertEquals(2, verifications)
    }

    @Test fun hostObserversCanDisappearWhileTheProcessOwnedDownloadFinishes() = runTest {
        val owner = CoroutineScope(SupervisorJob() + StandardTestDispatcher(testScheduler))
        val finished = CompletableDeferred<Unit>()
        val requested = CompletableDeferred<Unit>()
        val session = UpdateDownloadSession(owner, temporary.newFolder(), { _, file, _ ->
            requested.complete(Unit)
            finished.await()
            file.writeBytes(bytes)
        }, ::verify, StandardTestDispatcher(testScheduler))
        try {
            val observer = launch { session.state.collect { } }
            session.start(artifact())
            requested.await()
            observer.cancelAndJoin()
            assertEquals("downloading", session.state.value.status)
            finished.complete(Unit)
            advanceUntilIdle()
            assertEquals("ready", session.state.value.status)
            assertTrue(session.state.value.canInstall)
        } finally { owner.cancel() }
    }

    @Test fun permissionAndInstallerLaunchErrorsRetainTheVerifiedApkForExplicitRetry() = runTest {
        val directory = temporary.newFolder()
        val session = UpdateDownloadSession(this, directory, { _, file, _ -> file.writeBytes(bytes) }, ::verify, StandardTestDispatcher(testScheduler))
        session.start(artifact())
        advanceUntilIdle()
        val failure = session.install(750) { throw IOException("No installer available") }
        assertEquals("error", failure.status)
        assertTrue(failure.canInstall)
        assertEquals(1, directory.listFiles()!!.size)
        assertEquals("permissionRequired", session.install(750) { "permissionRequired" }.status)
        session.permissionGranted()
        assertEquals("ready", session.state.value.status)
        assertTrue(session.state.value.canInstall)
        assertEquals("ready", session.install(750) { "ready" }.status)
        assertTrue(runCatching { session.install(751) { error("Changed versions cannot reach the installer") } }.isFailure)
    }

    @Test fun cancellingAHostDuringInstallerHandoffRetainsTheVerifiedApkWithoutContinuing() = runTest {
        val session = UpdateDownloadSession(this, temporary.newFolder(), { _, file, _ -> file.writeBytes(bytes) }, ::verify, StandardTestDispatcher(testScheduler))
        session.start(artifact())
        advanceUntilIdle()
        val opened = CompletableDeferred<Unit>()
        val never = CompletableDeferred<Unit>()
        var handoffs = 0
        val host = launch {
            session.install(750) { handoffs++; opened.complete(Unit); never.await(); "ready" }
        }
        opened.await()
        assertEquals("installing", session.state.value.status)
        assertTrue(runCatching { session.cancel() }.isFailure)
        assertTrue(runCatching { session.start(artifact(751)) }.isFailure)
        host.cancelAndJoin()
        runCurrent()
        assertEquals("ready", session.state.value.status)
        assertTrue(session.state.value.canInstall)
        assertEquals(1, handoffs)
        session.install(750) { handoffs++; "ready" }
        assertEquals(2, handoffs)
    }

    @Test fun modifyingAPreparedApkInvalidatesItBeforeAnyNewInstallerRequest() = runTest {
        val directory = temporary.newFolder()
        val session = UpdateDownloadSession(this, directory, { _, file, _ -> file.writeBytes(bytes) }, ::verify, StandardTestDispatcher(testScheduler))
        session.start(artifact())
        advanceUntilIdle()
        var prepared: File? = null
        session.install(750) { file -> prepared = file; "ready" }
        prepared!!.writeBytes(ByteArray(bytes.size) { 9 })
        var handoffs = 0
        val result = session.install(750) { handoffs++; "ready" }
        assertEquals(0, handoffs)
        assertEquals("error", result.status)
        assertFalse(result.canInstall)
        assertFalse(prepared!!.exists())
        assertEquals(0, directory.listFiles()!!.size)
    }
}
