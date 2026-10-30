package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.backend.log.LogLevel
import io.nekohasekai.sfa.backend.log.LogUiState

/** A cursor belongs to one visible EventChannel subscription, never to a service. */
internal class LogStream {
    private var initialized = false
    private var firstId: Long? = null
    private var lastId: Long? = null
    private var level: LogLevel? = null

    fun reset() { initialized = false }

    fun update(state: LogUiState): Map<String, Any?>? {
        val first = state.logs.firstOrNull()?.id
        val last = state.logs.lastOrNull()?.id
        if (initialized && first == firstId && last == lastId && level == state.defaultLogLevel) return null

        val previousLast = lastId
        val reset = !initialized || last == null || previousLast != null && last < previousLast
        var start = state.logs.size
        if (reset || previousLast == null) {
            start = 0
        } else {
            // Usually only the newest batch is visited; retained entries are not re-encoded.
            while (start > 0 && state.logs[start - 1].id > previousLast) start--
        }
        val entries = state.logs.subList(start, state.logs.size).map {
            mapOf("id" to it.id, "level" to it.entry.level.label,
                "message" to it.entry.message, "receivedAt" to it.receivedAt)
        }
        initialized = true
        firstId = first
        lastId = last
        level = state.defaultLogLevel
        return mapOf("reset" to reset, "firstId" to first,
            "defaultLogLevel" to level?.label, "entries" to entries)
    }
}
