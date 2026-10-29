package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException
import org.junit.Assert.*
import org.junit.Test
import java.io.IOException

class DashboardConfigCacheTest {
    private val key = DashboardConfigCache.FileKey(1, "selected.json", 1, 100, 10, true)

    private class Fixture {
        var reads = 0
        var hashes = 0
        var parses = 0
        var content = "original"
        var readFailure: Exception? = null
        var parseFailure: Exception? = null
        val cache = DashboardConfigCache(
            revision = { hashes++; "revision:$it" },
            parseListeners = { content, addresses ->
                parses++
                parseFailure?.let { throw it }
                listOf(mapOf("content" to content, "addresses" to addresses))
            },
        )

        fun saved(key: DashboardConfigCache.FileKey) = cache.saved(key) {
            reads++
            readFailure?.let { throw it }
            content
        }
    }

    @Test fun unchangedPollsShareSavedAndActiveParsingWithoutReadingOrHashingAgain() {
        val fixture = Fixture()
        val active = Any()
        var activeReads = 0
        repeat(100) {
            val saved = fixture.saved(key)
            val running = fixture.cache.active(active) { activeReads++; "original" }
            assertSame(saved, running)
            assertSame(saved.listeners("wifi"), running.listeners("wifi"))
        }
        assertEquals(1, fixture.reads)
        assertEquals(1, activeReads)
        assertEquals(1, fixture.hashes)
        assertEquals(1, fixture.parses)
    }

    @Test fun addressChangesRegenerateEndpointsWithoutReadingOrHashingConfigurations() {
        val fixture = Fixture()
        val saved = fixture.saved(key)
        val running = fixture.cache.active(Any()) { "original" }
        assertEquals("wifi", saved.listeners("wifi").single()["addresses"])
        assertEquals("hotspot", fixture.saved(key).listeners("hotspot").single()["addresses"])
        assertSame(saved.listeners("hotspot"), running.listeners("hotspot"))
        assertEquals(1, fixture.reads)
        assertEquals(1, fixture.hashes)
        assertEquals(2, fixture.parses)
    }

    @Test fun equalSizeWritesAndRollbackDoNotReplaceTheRunningConfiguration() {
        val fixture = Fixture()
        val original = fixture.saved(key)
        val active = Any()
        fixture.cache.active(active) { "original" }.listeners("wifi")
        fixture.content = "modified"
        val edited = fixture.saved(key.copy(generation = 2))
        assertEquals("revision:modified", edited.revision)
        assertEquals("modified", edited.listeners("wifi").single()["content"])
        assertEquals("original", fixture.cache.active(active) { error("Unexpected active read") }
            .listeners("wifi").single()["content"])

        fixture.content = "original"
        val restored = fixture.saved(key.copy(generation = 3))
        assertSame(original, restored)
        assertEquals("original", restored.listeners("wifi").single()["content"])
        assertEquals(3, fixture.reads)
        assertEquals(2, fixture.hashes)
        assertEquals(2, fixture.parses)
    }

    @Test fun selectionPathAndExternalFileMetadataChangesReloadSavedContent() {
        val fixture = Fixture()
        fixture.saved(key)
        val changes = listOf(
            key.copy(profileId = 2),
            key.copy(profileId = 2, path = "restored.json"),
            key.copy(profileId = 2, path = "restored.json", modified = 101),
            key.copy(profileId = 2, path = "restored.json", modified = 101, size = 11),
        )
        for ((index, changed) in changes.withIndex()) {
            fixture.content = "configuration $index"
            assertEquals("revision:${fixture.content}", fixture.saved(changed).revision)
            fixture.saved(changed)
        }
        assertEquals(5, fixture.reads)
        assertEquals(5, fixture.hashes)
    }

    @Test fun unrelatedWritesReuseTheUnchangedRevisionAndListenerResult() {
        val fixture = Fixture()
        val original = fixture.saved(key)
        original.listeners("wifi")
        val next = fixture.saved(key.copy(generation = 2))
        assertSame(original, next)
        assertSame(original.listeners("wifi"), next.listeners("wifi"))
        assertEquals(2, fixture.reads)
        assertEquals(1, fixture.hashes)
        assertEquals(1, fixture.parses)
    }

    @Test fun aNewActiveSnapshotRefreshesOnlyActiveListeners() {
        val fixture = Fixture()
        val saved = fixture.saved(key)
        fixture.cache.active(Any()) { "original" }.listeners("wifi")
        val active = fixture.cache.active(Any()) { "reloaded" }
        assertEquals("reloaded", active.listeners("wifi").single()["content"])
        assertEquals("original", fixture.saved(key).listeners("wifi").single()["content"])
        fixture.cache.clearActive()
        assertSame(saved, fixture.cache.active(Any()) { "original" })
        assertEquals(1, fixture.reads)
        assertEquals(2, fixture.hashes)
        assertEquals(2, fixture.parses)
    }

    @Test fun failedReadsNeverPublishTheNewKeyOrReturnOldContent() {
        val fixture = Fixture()
        fixture.saved(key)
        fixture.readFailure = IOException("File unavailable")
        val missing = key.copy(exists = false, modified = 0, size = 0)
        assertEquals("File unavailable", runCatching { fixture.saved(missing) }.exceptionOrNull()!!.message)
        fixture.readFailure = null
        fixture.content = "recovered"
        assertEquals("revision:recovered", fixture.saved(missing).revision)
        assertEquals(3, fixture.reads)
        fixture.cache.clearSaved()
        fixture.saved(missing)
        assertEquals(4, fixture.reads)
    }

    @Test fun parserFailuresAreCachedAndManualRefreshRetriesThem() {
        val fixture = Fixture()
        fixture.parseFailure = IllegalArgumentException("Invalid configuration")
        repeat(5) {
            assertEquals("Invalid configuration", runCatching { fixture.saved(key).listeners("wifi") }
                .exceptionOrNull()!!.message)
        }
        assertEquals(1, fixture.reads)
        assertEquals(1, fixture.hashes)
        assertEquals(1, fixture.parses)
        fixture.parseFailure = null
        fixture.cache.refresh()
        assertEquals("original", fixture.saved(key).listeners("wifi").single()["content"])
        assertEquals(2, fixture.reads)
        assertEquals(1, fixture.hashes)
        assertEquals(2, fixture.parses)
    }

    @Test fun cancellationDoesNotPoisonTheParserCache() {
        val fixture = Fixture()
        fixture.parseFailure = CancellationException("Refresh cancelled")
        val config = fixture.saved(key)
        assertTrue(runCatching { config.listeners("wifi") }.exceptionOrNull() is CancellationException)
        fixture.parseFailure = null
        assertEquals("wifi", config.listeners("wifi").single()["addresses"])
        assertEquals(2, fixture.parses)
    }
}
