package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** One package snapshot, with labels loaded only after filtering, like SFA's PackageCache. */
internal class AppListCache(private val query: suspend () -> List<Entry>) {
    class Entry(
        val packageName: String,
        val system: Boolean,
        val internet: Boolean,
        val updatedAt: Long,
        loadLabel: () -> String,
    ) {
        val name by lazy(loadLabel)
    }

    private val mutex = Mutex()
    private var queryKey: String? = null
    private var packages: List<Entry>? = null

    suspend fun list(
        key: String,
        refresh: Boolean = false,
        showSystem: Boolean = false,
        showOffline: Boolean = false,
    ): List<Map<String, Any>> = mutex.withLock {
        if (refresh || key != queryKey || packages == null) {
            // A failed refresh must remain retryable, not silently reuse an old package snapshot.
            packages = null
            packages = query()
            queryKey = key
        }
        packages!!.asSequence()
            .filter { (showSystem || !it.system) && (showOffline || it.internet) }
            .map { entry ->
                mapOf(
                    "package" to entry.packageName,
                    "name" to entry.name,
                    "system" to entry.system,
                    "internet" to entry.internet,
                    "updatedAt" to entry.updatedAt,
                )
            }.toList()
    }
}
