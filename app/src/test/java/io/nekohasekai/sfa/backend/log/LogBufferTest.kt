package io.nekohasekai.sfa.backend.log

import org.junit.Assert.*
import org.junit.Test

class LogBufferTest {
    @Test fun retainsTraceAndDebugAndRemovesAnsiEscapes() {
        val buffer = LogBuffer(now = { 123L })
        buffer.append(listOf(LogEntryData(LogLevel.DEBUG, "\u001b[32mdebug\u001b[0m"), LogEntryData(LogLevel.TRACE, "trace")))
        assertEquals(listOf("debug", "trace"), buffer.snapshot().map { it.entry.message })
        assertEquals(listOf(LogLevel.DEBUG, LogLevel.TRACE), buffer.snapshot().map { it.entry.level })
        assertTrue(buffer.snapshot().all { it.receivedAt == 123L })
    }

    @Test fun oversizedBatchesStayBoundedAndIdsSurviveClear() {
        val buffer = LogBuffer(limit = 3)
        buffer.append(listOf(LogEntryData(LogLevel.INFO, "old")))
        buffer.append((1..10).map { LogEntryData(LogLevel.INFO, "$it") })
        val snapshot = buffer.snapshot()
        assertEquals(listOf("8", "9", "10"), snapshot.map { it.entry.message })
        buffer.clear()
        assertTrue(buffer.snapshot().isEmpty())
        buffer.append(listOf(LogEntryData(LogLevel.ERROR, "new")))
        assertTrue(buffer.snapshot().single().id > snapshot.last().id)
        assertEquals(3, snapshot.size)
    }
}
