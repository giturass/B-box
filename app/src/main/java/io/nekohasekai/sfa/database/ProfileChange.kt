package io.nekohasekai.sfa.database

import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext

/** Keep persisted profile data and the running service together on failure. */
internal object ProfileChange {
    suspend fun apply(
        save: suspend () -> Unit,
        restore: List<suspend () -> Unit>,
        applyService: suspend (recover: Boolean) -> Unit,
    ) {
        var applyAttempted = false
        try {
            save()
            applyAttempted = true
            applyService(false)
        } catch (error: Exception) {
            withContext(NonCancellable) {
                var restored = true
                for (task in restore) {
                    try {
                        task()
                    } catch (failure: Exception) {
                        restored = false
                        error.addSuppressed(failure)
                    }
                }
                if (applyAttempted && restored) {
                    runCatching { applyService(true) }.exceptionOrNull()?.let(error::addSuppressed)
                }
            }
            throw error
        }
    }
}
