package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.backend.log.LogEntryData
import io.nekohasekai.sfa.backend.log.LogLevel
import io.nekohasekai.sfa.backend.log.LogUiState
import io.nekohasekai.sfa.backend.log.ProcessedLogEntry
import org.junit.Assert.*
import org.junit.Test

class LogStreamTest {
    private fun state(vararg ids: Long, level: LogLevel? = LogLevel.INFO) = LogUiState(
        logs = ids.map { ProcessedLogEntry(it, LogEntryData(LogLevel.DEBUG, "record $it"), it * 1000) },
        defaultLogLevel = level,
    )

    @Suppress("UNCHECKED_CAST")
    private fun entries(update: Map<String, Any?>) = update["entries"] as List<Map<String, Any?>>

    @Test fun sendsSnapshotThenOnlyNewRecordsAndTheRetainedBoundary() {
        val stream = LogStream()
        val snapshot = stream.update(state(1, 2, 3))!!
        assertEquals(true, snapshot["reset"])
        assertEquals(listOf(1L, 2L, 3L), entries(snapshot).map { it["id"] })
        assertEquals("Debug", entries(snapshot).first()["level"])
        assertEquals(1000L, entries(snapshot).first()["receivedAt"])

        val delta = stream.update(state(2, 3, 4))!!
        assertEquals(false, delta["reset"])
        assertEquals(2L, delta["firstId"])
        assertEquals(listOf(4L), entries(delta).map { it["id"] })
        assertNull(stream.update(state(2, 3, 4)))
    }

    @Test fun logLevelChangesDoNotRetransmitTheBuffer() {
        val stream = LogStream()
        stream.update(state(1, 2))
        for (level in listOf(null, LogLevel.ERROR, LogLevel.TRACE)) {
            val update = stream.update(state(1, 2, level = level))!!
            assertEquals(level?.label, update["defaultLogLevel"])
            assertEquals(false, update["reset"])
            assertTrue(entries(update).isEmpty())
        }
    }

    @Test fun clearReconnectAndNewBuffersAlwaysResynchronize() {
        val stream = LogStream()
        stream.update(state(10, 11))
        val cleared = stream.update(state())!!
        assertEquals(true, cleared["reset"])
        assertNull(cleared["firstId"])
        assertTrue(entries(cleared).isEmpty())
        assertEquals(listOf(12L), entries(stream.update(state(12))!!).map { it["id"] })

        stream.reset()
        val resumed = stream.update(state(12, 13))!!
        assertEquals(true, resumed["reset"])
        assertEquals(2, entries(resumed).size)
        val replaced = stream.update(state(1))!!
        assertEquals(true, replaced["reset"])
        assertEquals(listOf(1L), entries(replaced).map { it["id"] })
    }

    @Test fun aFullBufferAddsOneEncodedRecordPerUpdate() {
        val stream = LogStream()
        stream.update(state(*(1L..3000L).toList().toLongArray()))
        val update = stream.update(state(*(2L..3001L).toList().toLongArray()))!!
        assertEquals(2L, update["firstId"])
        assertEquals(1, entries(update).size)
    }
}
