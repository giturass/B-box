package io.nekohasekai.sfa.backend.connections

import io.nekohasekai.sfa.backend.model.Connection

/** Immutable snapshots let a conflated UI consumer compare against its last delivery. */
internal class ConnectionStore {
    enum class Type { NEW, UPDATE, CLOSED }
    data class Event(
        val type: Type,
        val id: String,
        val connection: Connection? = null,
        val upload: Long = 0,
        val download: Long = 0,
        val closedAt: Long = 0,
    )

    private var snapshot: Map<String, Connection> = emptyMap()

    fun clear(): Map<String, Connection> = emptyMap<String, Connection>().also { snapshot = it }

    // Matches libbox command_types.go at v1.15.0-alpha.13, including the five-minute
    // closed history and final totals supplied by CLOSED events.
    fun apply(reset: Boolean, events: Sequence<Event>, now: Long): Map<String, Connection> {
        var changed: MutableMap<String, Connection>? = if (reset) linkedMapOf() else null
        fun current() = changed ?: snapshot
        fun mutable() = changed ?: snapshot.toMutableMap().also { changed = it }
        for (event in events) {
            val previous = current()[event.id]
            val next = when (event.type) {
                Type.NEW -> event.connection ?: continue
                Type.UPDATE -> previous?.copy(
                    upload = event.upload, download = event.download,
                    uploadTotal = previous.uploadTotal + event.upload,
                    downloadTotal = previous.downloadTotal + event.download,
                ) ?: continue
                Type.CLOSED -> (event.connection ?: previous)?.copy(
                    closedAt = event.closedAt.takeIf { it > 0 }, upload = 0, download = 0,
                ) ?: continue
            }
            if (next.outboundType == "dns") {
                if (previous != null) mutable().remove(event.id)
            } else if (next != previous) {
                mutable()[event.id] = next
            }
        }
        val expired = current().filterValues { it.closedAt?.let { closed -> now - closed > 300_000 } == true }.keys
        if (expired.isNotEmpty()) mutable().keys.removeAll(expired)
        return (changed ?: snapshot).also { snapshot = it }
    }
}
