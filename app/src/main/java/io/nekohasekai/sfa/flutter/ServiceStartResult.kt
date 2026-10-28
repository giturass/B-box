package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.withTimeout

/** A service command is acknowledged only when Android reports Started. */
internal class ServiceStartResult {
    @Volatile private var pending: CompletableDeferred<Unit>? = null

    suspend fun awaitStarted(timeoutMillis: Long = 120_000, start: () -> Unit) {
        check(pending == null) { "A service start is already pending" }
        val result = CompletableDeferred<Unit>()
        pending = result
        try {
            start()
            withTimeout(timeoutMillis) { result.await() }
        } finally {
            if (pending === result) pending = null
            result.cancel()
        }
    }

    fun started() { pending?.complete(Unit) }
    fun failed(error: Throwable) { pending?.completeExceptionally(error) }
}
