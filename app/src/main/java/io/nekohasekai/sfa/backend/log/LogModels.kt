package io.nekohasekai.sfa.backend.log

data class LogEntryData(val level: LogLevel, val message: String)
data class ProcessedLogEntry(val id: Long, val entry: LogEntryData, val receivedAt: Long)
data class LogUiState(
    val logs: List<ProcessedLogEntry> = emptyList(),
    val defaultLogLevel: LogLevel? = null,
)

enum class LogLevel(val label: String, val priority: Int) {
    Default("Default", 7), PANIC("Panic", 0), FATAL("Fatal", 1), ERROR("Error", 2),
    WARNING("Warn", 3), INFO("Info", 4), DEBUG("Debug", 5), TRACE("Trace", 6);

    companion object {
        fun from(priority: Int) = entries.firstOrNull { it.priority == priority } ?: Default
    }
}

/** Bounded, unfiltered records. Filtering and pause snapshots belong to Flutter. */
internal class LogBuffer(private val limit: Int = 3000, private val now: () -> Long = System::currentTimeMillis) {
    private val records = ArrayDeque<ProcessedLogEntry>()
    private var nextId = 0L
    private val ansi = Regex("\u001B\\[[;\\d]*m")

    init { require(limit > 0) }

    fun append(entries: List<LogEntryData>) {
        for (entry in entries.takeLast(limit)) {
            if (records.size == limit) records.removeFirst()
            records.addLast(ProcessedLogEntry(++nextId, entry.copy(message = entry.message.replace(ansi, "")), now()))
        }
    }

    fun clear() = records.clear()
    fun snapshot(): List<ProcessedLogEntry> = records.toList()
}
