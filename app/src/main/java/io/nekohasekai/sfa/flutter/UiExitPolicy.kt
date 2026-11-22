package io.nekohasekai.sfa.flutter

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.yield
import java.io.Closeable
import java.util.Collections
import java.util.IdentityHashMap
import java.util.concurrent.atomic.AtomicBoolean

internal fun isUiProcessName(packageName: String, processName: String?): Boolean =
    processName == "$packageName:ui"

/**
 * Keep the UI alive for five minutes of continuous background idle.
 * A stopped Activity alone cannot distinguish backgrounding from an external result or recreation.
 */
internal class UiExitPolicy(
    private val scope: CoroutineScope,
    private val isUiProcess: () -> Boolean,
    private val beforeExit: suspend () -> Unit,
    private val exit: () -> Unit,
    private val onFailure: (Exception) -> Unit = {},
    private val idleTimeoutMillis: Long = 5 * 60 * 1_000L,
    private val awaitIdle: suspend (Long) -> Closeable = { delay(it); Closeable {} },
    private val awaitExitDispatch: suspend () -> Unit = { yield() },
) : Closeable {
    private val access = Any()
    private val started = Collections.newSetFromMap(IdentityHashMap<Any, Boolean>())
    private val recreating = mutableSetOf<String>()
    private var everStarted = false
    private var leases = 0
    private var generation = 0L
    private var pendingExit: Job? = null
    private var closed = false

    fun activityStarted(activity: Any, configurationId: String) = synchronized(access) {
        if (started.add(activity)) {
            recreating.remove(configurationId)
            everStarted = true
            changed()
        }
    }

    fun activityStopped(activity: Any, changingConfigurationId: String? = null) = synchronized(access) {
        if (started.remove(activity)) {
            if (changingConfigurationId != null) recreating.add(changingConfigurationId)
            changed()
        }
    }

    fun acquireLease(): Closeable {
        synchronized(access) {
            leases++
            changed()
        }
        val released = AtomicBoolean()
        return Closeable {
            if (released.compareAndSet(false, true)) synchronized(access) {
                leases--
                changed()
            }
        }
    }

    private fun canExit(): Boolean =
        !closed && everStarted && started.isEmpty() && recreating.isEmpty() && leases == 0 && isUiProcess()

    /** Every change invalidates both the timer and an in-flight drain. */
    private fun changed() {
        generation++
        pendingExit?.cancel()
        pendingExit = null
        if (!canExit()) return
        val expectedGeneration = generation
        val job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                val execution = awaitIdle(idleTimeoutMillis)
                try {
                    synchronized(access) {
                        if (generation != expectedGeneration || !canExit()) return@launch
                    }
                    beforeExit()
                } finally {
                    execution.close()
                }
                // JobService posts jobFinished to its main Handler. Let that message notify
                // Android before killing this process, then recheck any foreground return.
                awaitExitDispatch()
                currentCoroutineContext().ensureActive()
                // Draining the bridge may suspend. Recheck foreground, leases and process identity.
                synchronized(access) {
                    if (generation == expectedGeneration && canExit()) exit()
                }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                onFailure(error)
            } finally {
                synchronized(access) {
                    if (generation == expectedGeneration) pendingExit = null
                }
            }
        }
        pendingExit = job
        job.start()
    }

    override fun close() = synchronized(access) {
        closed = true
        changed()
    }
}
