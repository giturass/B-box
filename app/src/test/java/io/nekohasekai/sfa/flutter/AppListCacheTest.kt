package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import java.io.IOException

@OptIn(ExperimentalCoroutinesApi::class)
class AppListCacheTest {
    @Test fun filtersBeforeLoadingLabelsAndReusesTheSnapshotWhenFiltersChange() = runTest {
        var queries = 0
        val reads = mutableMapOf<String, Int>()
        fun entry(name: String, system: Boolean = false, internet: Boolean = true) =
            AppListCache.Entry(name, system, internet, 1) {
                reads[name] = (reads[name] ?: 0) + 1
                name
            }
        val cache = AppListCache {
            queries++
            listOf(entry("visible"), entry("system", system = true), entry("offline", internet = false))
        }
        repeat(5) {
            assertEquals(listOf("visible"), cache.list("direct").map { it["package"] })
        }
        assertEquals(mapOf("visible" to 1), reads)
        assertEquals(listOf("visible", "system"), cache.list("direct", showSystem = true).map { it["package"] })
        assertEquals(mapOf("visible" to 1, "system" to 1), reads)
        assertEquals(3, cache.list("direct", showSystem = true, showOffline = true).size)
        assertEquals(mapOf("visible" to 1, "system" to 1, "offline" to 1), reads)
        assertEquals(1, queries)
    }

    @Test fun refreshAndQueryOrLocaleChangesReplacePackagesAndLabels() = runTest {
        var queries = 0
        val cache = AppListCache {
            val generation = ++queries
            listOf(AppListCache.Entry("app.$generation", false, true, generation.toLong()) { "Label $generation" })
        }
        assertEquals("Label 1", cache.list("direct:en").single()["name"])
        assertEquals("app.2", cache.list("direct:en", refresh = true).single()["package"])
        assertEquals("Label 3", cache.list("root:en").single()["name"])
        assertEquals("Label 4", cache.list("root:zh").single()["name"])
        assertEquals(4, queries)
    }

    @Test fun failedOrCancelledRefreshesAreRetriedWithoutReturningOldPackages() = runTest {
        var failure: Exception? = null
        var queries = 0
        val cache = AppListCache {
            val generation = ++queries
            failure?.let { throw it }
            listOf(AppListCache.Entry("app.$generation", false, true, 0) { "App" })
        }
        cache.list("direct")
        for (error in listOf(IOException("Package query failed"), CancellationException("Cancelled"))) {
            failure = error
            assertSame(error, runCatching { cache.list("direct", refresh = true) }.exceptionOrNull())
            failure = null
            assertEquals("app.${queries + 1}", cache.list("direct").single()["package"])
        }
        assertEquals(5, queries)
    }

    @Test fun overlappingFilterRequestsShareOnePendingQuery() = runTest {
        val ready = CompletableDeferred<Unit>()
        var queries = 0
        val cache = AppListCache {
            queries++
            ready.await()
            listOf(AppListCache.Entry("system", true, true, 0) { "System app" })
        }
        val hidden = async { cache.list("direct", refresh = true) }
        runCurrent()
        val shown = async { cache.list("direct", showSystem = true) }
        runCurrent()
        assertEquals(1, queries)
        ready.complete(Unit)
        assertTrue(hidden.await().isEmpty())
        assertEquals("System app", shown.await().single()["name"])
        assertEquals(1, queries)
    }
}
