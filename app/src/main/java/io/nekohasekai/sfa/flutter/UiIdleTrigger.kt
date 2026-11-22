package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.job
import java.io.Closeable
import java.util.UUID

/** Main-thread gate between a system wakeup and the current UI process's idle timer. */
internal class UiIdleTrigger(
    private val nowMillis: () -> Long,
    private val schedule: (token: String, deadlineMillis: Long, delayMillis: Long) -> Unit,
    private val cancel: () -> Unit,
) {
    private class Pending(val token: String, val deadlineMillis: Long, val owner: Job) {
        val ready = CompletableDeferred<Closeable>()
        var finish: (() -> Unit)? = null
    }

    private var pending: Pending? = null

    suspend fun awaitIdle(delayMillis: Long): Closeable {
        val deadlineMillis = nowMillis() + delayMillis
        pending?.let {
            it.owner.cancel(CancellationException("UI idle timer was replaced"))
            // Keep an already running system job bound until recovery finishes, even if
            // the user returns to the foreground and immediately backgrounds again.
            it.owner.join()
            release(it, cancelScheduled = true)
        }
        // A fresh token also rejects jobs left behind by an earlier UI process.
        val request = Pending(UUID.randomUUID().toString(), deadlineMillis, currentCoroutineContext().job)
        pending = request
        try {
            schedule(request.token, request.deadlineMillis, (deadlineMillis - nowMillis()).coerceAtLeast(0))
            return request.ready.await()
        } catch (error: Throwable) {
            release(request, cancelScheduled = true)
            throw error
        }
    }

    fun start(token: String?, deadlineMillis: Long, finish: () -> Unit): Boolean {
        val request = matching(token, deadlineMillis) ?: return false
        if (!request.owner.isActive || request.ready.isCompleted) return false
        if (nowMillis() < request.deadlineMillis) {
            request.ready.completeExceptionally(IllegalStateException("UI idle job arrived before its deadline"))
            return false
        }
        request.finish = finish
        return request.ready.complete(Closeable { release(request, cancelScheduled = false) })
    }

    fun stop(token: String?, deadlineMillis: Long) {
        val request = matching(token, deadlineMillis) ?: return
        pending = null
        // onStopJob already finishes this execution; it must not get a second jobFinished.
        request.finish = null
        request.owner.cancel(CancellationException("UI idle job was stopped"))
        request.ready.cancel()
    }

    private fun matching(token: String?, deadlineMillis: Long): Pending? =
        pending?.takeIf { it.token == token && it.deadlineMillis == deadlineMillis }

    private fun release(request: Pending, cancelScheduled: Boolean) {
        val finish = request.finish
        request.finish = null
        if (pending === request) {
            pending = null
            if (cancelScheduled && finish == null) cancel()
        }
        finish?.invoke()
    }
}
