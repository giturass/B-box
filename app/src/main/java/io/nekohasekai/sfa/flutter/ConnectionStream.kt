package io.nekohasekai.sfa.flutter

import io.nekohasekai.sfa.backend.model.Connection

/** Resynchronizes on visibility changes; skipped StateFlow snapshots cannot lose deltas. */
internal class ConnectionStream {
    private var previous: Map<String, Connection>? = null

    fun reset() { previous = null }

    fun update(snapshot: Map<String, Connection>): Map<String, Any?>? {
        val old = previous
        if (old === snapshot) return null
        val entries = snapshot.filter { (id, value) -> old?.get(id) !== value }.values.map { it.toWire() }
        val removed = old?.keys?.filter { it !in snapshot }.orEmpty()
        previous = snapshot
        if (old != null && entries.isEmpty() && removed.isEmpty()) return null
        return mapOf("reset" to (old == null), "entries" to entries, "removed" to removed)
    }
}
