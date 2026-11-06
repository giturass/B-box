package io.nekohasekai.sfa.utils

import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext

/** Do not leave a failed module update or VPN restart saved as a successful edit. */
internal object PrivilegeSettingsChange {
    suspend fun apply(
        previous: PrivilegeSettingsConfig,
        next: PrivilegeSettingsConfig,
        save: suspend (PrivilegeSettingsConfig) -> Unit,
        sync: suspend (PrivilegeSettingsConfig) -> Unit,
        restart: suspend (recover: Boolean) -> Unit,
    ) {
        var syncAttempted = false
        var restartAttempted = false
        try {
            save(next)
            syncAttempted = true
            sync(next)
            restartAttempted = true
            restart(false)
        } catch (error: Exception) {
            withContext(NonCancellable) {
                val restoreError = runCatching { save(previous) }.exceptionOrNull()
                restoreError?.let(error::addSuppressed)
                val syncError = if (syncAttempted) runCatching { sync(previous) }.exceptionOrNull() else null
                syncError?.let(error::addSuppressed)
                if (restartAttempted && restoreError == null && syncError == null) {
                    runCatching { restart(true) }.exceptionOrNull()?.let(error::addSuppressed)
                }
            }
            throw error
        }
    }
}
