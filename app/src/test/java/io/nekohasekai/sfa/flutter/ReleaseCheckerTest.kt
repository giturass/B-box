package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import java.io.IOException

@OptIn(ExperimentalCoroutinesApi::class)
class ReleaseCheckerTest {
    private fun release(id: Int, revision: String = "initial") = ReleaseChecker.Release(
        "https://example.test/releases/$id", "Notes $id",
        listOf(ReleaseChecker.Asset("B-box-version-metadata.json", "https://example.test/$id", "$id:$revision")),
    )
    private fun metadata(code: Long, app: String = "B-box") = ReleaseChecker.Metadata(app, code, "v$code")

    @Test fun aFailedAssetPreservesTheHighestConfirmedCandidate() = runTest {
        val checker = ReleaseChecker("B-box", { listOf(release(1), release(2), release(3), release(4)) }, { asset, _ ->
            when (asset.url.substringAfterLast('/')) {
                "1" -> metadata(12)
                "2" -> throw IOException("One release is unavailable")
                "3" -> metadata(9)
                else -> metadata(99, "another.app")
            }
        }, clock = { testScheduler.currentTime })
        val result = checker.check()
        assertFalse(result.complete)
        assertEquals(12L, result.candidate?.metadata?.versionCode)
    }

    @Test fun slowRequestsShareOneDeadlineAndKeepCompletedCandidates() = runTest {
        var active = 0
        var maxActive = 0
        var requests = 0
        val checker = ReleaseChecker("B-box", { delay(2_000); (0..19).map { release(it) } }, { asset, timeout ->
            assertTrue(timeout in 1L..8_000L)
            requests++
            active++
            maxActive = maxOf(maxActive, active)
            try {
                delay(if (asset.url.endsWith("/0")) 1_000 else 30_000)
                metadata(10)
            } finally { active-- }
        }, clock = { testScheduler.currentTime })
        val result = checker.check()
        assertFalse(result.complete)
        assertEquals(10L, result.candidate?.metadata?.versionCode)
        assertTrue(testScheduler.currentTime <= 20_000)
        assertTrue(maxActive <= 3)
        assertEquals(0, active)
        assertTrue(requests < 20)
    }

    @Test fun concurrentChecksShareCacheAndChangedAssetsAreFetchedAgain() = runTest {
        var listRequests = 0
        var metadataRequests = 0
        var revision = "initial"
        val checker = ReleaseChecker("B-box", { listRequests++; listOf(release(1, revision), release(2)) }, { asset, _ ->
            metadataRequests++
            delay(100)
            metadata(if (asset.cacheKey.endsWith("changed")) 20 else 10)
        }, clock = { testScheduler.currentTime })
        val results = List(3) { async { checker.check() } }.awaitAll()
        assertTrue(results.all { it.complete })
        assertEquals(1, listRequests)
        assertEquals(2, metadataRequests)
        revision = "changed"
        advanceTimeBy(60_001)
        val changed = checker.check()
        assertEquals(20L, changed.candidate?.metadata?.versionCode)
        assertEquals(2, listRequests)
        assertEquals(3, metadataRequests) // The unchanged asset is reused.
    }

    @Test fun unavailableListNeverTurnsAnExpiredResultIntoAnUpToDateClaim() = runTest {
        var offline = false
        val checker = ReleaseChecker("B-box", {
            if (offline) throw IOException("Offline")
            listOf(release(1))
        }, { _, _ -> metadata(10) }, clock = { testScheduler.currentTime })
        assertTrue(checker.check().complete)
        advanceTimeBy(60_001)
        offline = true
        val partial = checker.check()
        assertFalse(partial.complete)
        assertEquals(10L, partial.candidate?.metadata?.versionCode)
    }

    @Test fun cancellationReleasesTheLookupForRetry() = runTest {
        var waiting = true
        val requested = CompletableDeferred<Unit>()
        val checker = ReleaseChecker("B-box", { listOf(release(1)) }, { _, _ ->
            requested.complete(Unit)
            if (waiting) CompletableDeferred<Unit>().await()
            metadata(10)
        }, clock = { testScheduler.currentTime })
        val task = launch { checker.check() }
        requested.await()
        task.cancelAndJoin()
        waiting = false
        runCurrent()
        assertTrue(checker.check().complete)
    }

    @Test fun applicationVersionResetIsOrderedByAndroidVersionCode() = runTest {
        val checker = ReleaseChecker("B-box", { listOf(release(1), release(2)) }, { asset, _ ->
            if (asset.url.endsWith("/1")) ReleaseChecker.Metadata("B-box", 748, "1.15.0-alpha.13")
            else ReleaseChecker.Metadata("B-box", 749, "1.0.1")
        }, clock = { testScheduler.currentTime })
        val result = checker.check()
        assertTrue(result.complete)
        assertEquals("1.0.1", result.candidate?.metadata?.version)
        assertEquals(749L, result.candidate?.metadata?.versionCode)
    }

    @Test fun releasesWithoutMetadataCannotProduceAnUpToDateClaim() = runTest {
        val checker = ReleaseChecker("B-box", { listOf(ReleaseChecker.Release("https://example.test/latest", "", emptyList())) },
            { _, _ -> error("No metadata should be requested") }, clock = { testScheduler.currentTime })
        val result = checker.check()
        assertFalse(result.complete)
        assertNull(result.candidate)
    }

    @Test fun aPublicReleaseMissingMetadataKeepsAnOlderConfirmedCandidateIncomplete() = runTest {
        val missing = ReleaseChecker.Release("https://example.test/new-release", "New release", emptyList())
        var requests = 0
        val checker = ReleaseChecker("B-box", { listOf(missing, release(1)) }, { _, _ -> requests++; metadata(748) },
            clock = { testScheduler.currentTime })
        val result = checker.check()
        assertEquals(1, requests)
        assertEquals(748L, result.candidate?.metadata?.versionCode)
        assertFalse(result.complete)
        assertFalse(checker.check().complete) // Cached old metadata cannot hide the unchecked release.
    }

    @Test fun anotherApplicationMetadataDoesNotCompleteABboxReleaseLookup() = runTest {
        val checker = ReleaseChecker("B-box", { listOf(release(2), release(1)) }, { asset, _ ->
            if (asset.url.endsWith("/2")) metadata(999, "another.application") else metadata(749)
        }, clock = { testScheduler.currentTime })
        val result = checker.check()
        assertEquals(749L, result.candidate?.metadata?.versionCode)
        assertFalse(result.complete)
        assertFalse(checker.check().complete)
    }

    @Test fun duplicatedMetadataAssetsCannotAuthorizeACompleteLookup() = runTest {
        val base = release(1)
        val checker = ReleaseChecker("B-box", { listOf(base.copy(assets = base.assets + base.assets)) },
            { _, _ -> error("Ambiguous metadata must not be requested") }, clock = { testScheduler.currentTime })
        val result = checker.check()
        assertFalse(result.complete)
        assertNull(result.candidate)
    }

    @Test fun aConfirmedPartialCandidateSurvivesAListFailure() = runTest {
        var offline = false
        val checker = ReleaseChecker("B-box", {
            if (offline) throw IOException("Offline")
            listOf(release(1), release(2))
        }, { asset, _ ->
            if (asset.url.endsWith("/2")) throw IOException("Metadata unavailable")
            metadata(750)
        }, clock = { testScheduler.currentTime })
        assertFalse(checker.check().complete)
        offline = true
        val fallback = checker.check()
        assertFalse(fallback.complete)
        assertEquals(750L, fallback.candidate?.metadata?.versionCode)
    }

    @Test fun downloadSelectionRequiresOneCompatibleAssetAndMatchingIntegrityMetadata() {
        val digest = "a".repeat(64)
        fun candidate(assets: List<ReleaseChecker.Asset>, artifacts: List<ReleaseChecker.Artifact>) = ReleaseChecker.Candidate(
            ReleaseChecker.Release("https://example.test/release", "", assets),
            ReleaseChecker.Metadata("B-box", 750, "1.0.2", artifacts),
        )
        val armName = "B-box-1.0.2-arm64-v8a.apk"
        val x64Name = "B-box-1.0.2-x86_64.apk"
        val arm = ReleaseChecker.Asset(armName, "https://example.test/arm", size = 42)
        val x64 = ReleaseChecker.Asset(x64Name, "https://example.test/x64", size = 43)
        val armHash = ReleaseChecker.Artifact(armName, digest, 42)
        val x64Hash = ReleaseChecker.Artifact(x64Name, digest, 43)
        assertEquals(arm, ReleaseChecker.artifact(candidate(listOf(x64, arm), listOf(x64Hash, armHash)), listOf("arm64-v8a", "x86_64"))?.first)
        assertNull(ReleaseChecker.artifact(candidate(listOf(arm), emptyList()), listOf("arm64-v8a")))
        assertNull(ReleaseChecker.artifact(candidate(listOf(arm), listOf(armHash.copy(size = 41))), listOf("arm64-v8a")))
        assertNull(ReleaseChecker.artifact(candidate(listOf(arm), listOf(armHash.copy(sha256 = "invalid"))), listOf("arm64-v8a")))
        assertNull(ReleaseChecker.artifact(candidate(listOf(arm, arm), listOf(armHash)), listOf("arm64-v8a")))
        assertNull(ReleaseChecker.artifact(candidate(listOf(arm), listOf(armHash, armHash)), listOf("arm64-v8a")))
        assertNull(ReleaseChecker.artifact(candidate(listOf(arm), listOf(armHash)), listOf("x86_64")))
    }
}
