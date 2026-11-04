package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.backend.connections.ConnectionStore
import io.nekohasekai.sfa.backend.connections.ConnectionStore.Event
import io.nekohasekai.sfa.backend.connections.ConnectionStore.Type
import io.nekohasekai.sfa.backend.model.Connection
import org.junit.Assert.*
import org.junit.Test

class ConnectionStreamTest {
    private fun connection(id: String) = Connection(
        id, "mixed", "mixed", 4, "tcp", "127.0.0.1:1234", "example.test:443", "example.test",
        "example.test:443", "tls", "", "", 1000L, null, 0, 0, 10, 20,
        "final", "direct", "direct", listOf("direct"), null,
    )
    @Suppress("UNCHECKED_CAST")
    private fun entries(update: Map<String, Any?>) = update["entries"] as List<Map<String, Any?>>

    @Test fun updatesReuseUnchangedRowsAndPreservePreviouslyDeliveredSnapshots() {
        val store = ConnectionStore()
        val first = store.apply(true, sequenceOf(Event(Type.NEW, "a", connection("a")), Event(Type.NEW, "b", connection("b"))), 1000)
        val second = store.apply(false, sequenceOf(Event(Type.UPDATE, "a", upload = 3, download = 5)), 2000)
        assertSame(first["b"], second["b"])
        assertEquals(10L, first.getValue("a").uploadTotal)
        assertEquals(13L, second.getValue("a").uploadTotal)
        assertEquals(25L, second.getValue("a").downloadTotal)
        assertSame(second, store.apply(false, emptySequence(), 3000))
        assertSame(second, store.apply(false, sequenceOf(Event(Type.UPDATE, "missing", upload = 100)), 3000))
    }

    @Test fun closeUsesFinalTotalsAndEvictsOnlyExpiredHistory() {
        val store = ConnectionStore()
        store.apply(true, sequenceOf(Event(Type.NEW, "a", connection("a"))), 1000)
        val closed = store.apply(false, sequenceOf(Event(Type.CLOSED, "a",
            connection("a").copy(uploadTotal = 99, downloadTotal = 150, upload = 80), closedAt = 2000)), 2000)
        assertEquals(99L, closed.getValue("a").uploadTotal)
        assertEquals(150L, closed.getValue("a").downloadTotal)
        assertEquals(0L, closed.getValue("a").upload)
        assertEquals(2000L, closed.getValue("a").closedAt)
        assertSame(closed, store.apply(false, emptySequence(), 302_000))
        assertTrue(store.apply(false, emptySequence(), 302_001).isEmpty())
        val active = store.apply(false, sequenceOf(Event(Type.NEW, "a", connection("a"))), 500_000)
        assertFalse(active.isEmpty())
        assertTrue(store.apply(true, emptySequence(), 500_001).isEmpty())
        assertFalse(active.isEmpty())
    }

    @Test fun hiddenDNSConnectionsAndRemovedSessionsCannotLeakIntoSnapshots() {
        val store = ConnectionStore()
        val first = store.apply(true, sequenceOf(Event(Type.NEW, "dns", connection("dns").copy(outboundType = "dns")),
            Event(Type.NEW, "a", connection("a"))), 1000)
        assertEquals(setOf("a"), first.keys)
        store.clear()
        assertTrue(store.apply(false, sequenceOf(Event(Type.UPDATE, "a", upload = 10)), 1000).isEmpty())
        val closed = store.apply(false, sequenceOf(Event(Type.CLOSED, "a", connection("a"), closedAt = 1000)), 2000)
        assertEquals(setOf("a"), closed.keys)
    }

    @Test fun aLargeSnapshotSendsOnlyChangesAndSurvivesConflationAndVisibilityResets() {
        val store = ConnectionStore()
        val stream = ConnectionStream()
        val first = store.apply(true, (1..3000).asSequence().map { Event(Type.NEW, "$it", connection("$it")) }, 1000)
        assertEquals(3000, entries(stream.update(first)!!).size)
        store.apply(false, sequenceOf(Event(Type.UPDATE, "1", upload = 1)), 2000)
        val latest = store.apply(false, sequenceOf(Event(Type.UPDATE, "2", download = 2)), 3000)
        val delta = stream.update(latest)!!
        assertEquals(false, delta["reset"])
        assertEquals(setOf("1", "2"), entries(delta).map { it["id"] }.toSet())
        assertEquals(11L, entries(delta).first { it["id"] == "1" }["uploadTotal"])
        assertNull(stream.update(latest))
        stream.reset()
        val resumed = stream.update(latest)!!
        assertEquals(true, resumed["reset"])
        assertEquals(3000, entries(resumed).size)
        val removed = stream.update(store.clear())!!
        assertEquals(3000, (removed["removed"] as List<*>).size)
        assertTrue(entries(removed).isEmpty())
    }
}
