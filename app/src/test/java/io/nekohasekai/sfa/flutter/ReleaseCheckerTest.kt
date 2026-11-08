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
}
