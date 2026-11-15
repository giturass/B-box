package io.nekohasekai.sfa.database

import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext

/** Keep persisted profile data and the running service together on failure. */
internal object ProfileChange {
    suspend fun apply(
        save: suspend () -> Unit,
        restore: List<suspend () -> Unit>,
        // Null restores without a barrier. A suspend lambda as the default crashes the compiler
        // when a caller is lowered before this file.
        restoreBarrier: (suspend (suspend () -> Unit) -> Unit)? = null,
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
                val restoreAll: suspend () -> Unit = {
                    for (task in restore) {
                        try {
                            task()
                        } catch (failure: Exception) {
                            restored = false
                            error.addSuppressed(failure)
                        }
                    }
                }
                try {
                    if (restoreBarrier == null) restoreAll() else restoreBarrier(restoreAll)
                } catch (failure: Exception) {
                    restored = false
                    error.addSuppressed(failure)
                }
                if (applyAttempted && restored) {
                    runCatching { applyService(true) }.exceptionOrNull()?.let(error::addSuppressed)
                }
            }
            throw error
        }
    }
}
