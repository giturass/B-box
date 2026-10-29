package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext

/** Save a service setting and acknowledge its restart, restoring it on failure. */
internal object ServiceSettingsChange {
    suspend fun apply(
        previous: Map<String, Any?>,
        values: Map<String, Any?>,
        save: suspend (Map<String, Any?>) -> Unit,
        restart: suspend (recover: Boolean) -> Unit,
    ) {
        var restartAttempted = false
        try {
            save(values)
            restartAttempted = true
            restart(false)
        } catch (error: Exception) {
            withContext(NonCancellable) {
                runCatching { save(previous) }.exceptionOrNull()?.let(error::addSuppressed)
                if (restartAttempted) {
                    runCatching { restart(true) }.exceptionOrNull()?.let(error::addSuppressed)
                }
            }
            throw error
        }
    }
}
